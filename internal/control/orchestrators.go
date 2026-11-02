package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// firstRun is what a worker's first start counts as.
const firstRun = 1

// Orchestrator reads a worker's orchestrator. A worker no member started one on has none.
func (w *Workers) Orchestrator(ctx context.Context, worker string) (ledger.Orchestrator, error) {
	var row ledger.Orchestrator
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.Workers().Orchestrator(worker)
		return err
	})
	return row, err
}

// SetOrchestrator records what a member asks of a worker's orchestrator. A start of one that is
// running asks for nothing new, unless its breaker tripped, which only a person may reset.
func (w *Workers) SetOrchestrator(ctx context.Context, worker, desired string, actor ledger.Actor) (ledger.Orchestrator, error) {
	if !authority.Allowed(actor, authority.ActionRunOrchestrator) {
		return ledger.Orchestrator{}, workorders.ErrForbidden
	}
	if desired != ledger.OrchestratorRunning && desired != ledger.OrchestratorStopped {
		return ledger.Orchestrator{}, fmt.Errorf("%w: an orchestrator cannot be %q", workorders.ErrInvalid, desired)
	}
	var row ledger.Orchestrator
	err := w.store.Write(ctx, w.scope, "orchestrator.set", func(tx ports.WriteTx) error {
		if _, err := tx.Workers().Find(worker); err != nil {
			return err
		}
		current, err := tx.Workers().Orchestrator(worker)
		if errors.Is(err, ports.ErrNotFound) {
			current = ledger.Orchestrator{ID: ledger.OrchestratorOf(worker), WorkerID: worker, Desired: ledger.OrchestratorStopped}
		} else if err != nil {
			return err
		}
		row = current
		switch {
		case desired == ledger.OrchestratorRunning && current.Desired == ledger.OrchestratorRunning && current.Actual == "tripped":
			row.Resets++
		case desired == current.Desired:
			return nil
		case desired == ledger.OrchestratorRunning:
			row.Run, row.Desired, row.Actual, row.Generation = max(current.Run+1, firstRun), desired, "", 0
		default:
			row.Desired = desired
		}
		if err := tx.Workers().SaveOrchestrator(row); err != nil {
			return err
		}
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: ledger.EventOrchestratorDesired, Actor: actor, Data: data,
			Subject: ledger.Subject{Kind: ledger.SubjectOrchestrator, ID: row.ID},
		})
		return err
	})
	return row, err
}

func desiredOrchestrator(tx ports.ReadTx, worker string) (*ledger.DesiredOrchestrator, error) {
	row, err := tx.Workers().Orchestrator(worker)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	if err != nil || row.Desired != ledger.OrchestratorRunning {
		return nil, err
	}
	return &ledger.DesiredOrchestrator{Lane: row.Lane(), Resets: row.Resets}, nil
}

// orchestratorUpload stores what a worker says of its orchestrator. There is no fence, because there
// is no work order: a worker speaks for its own, and a word about a start this one replaced is kept
// and moves nothing.
func orchestratorUpload(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	row, err := tx.Workers().Orchestrator(worker)
	if errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("%w: worker %s has no orchestrator", workorders.ErrInvalid, worker)
	}
	if err != nil {
		return err
	}
	if !row.Belongs(e.Lane) {
		return fmt.Errorf("%w: %s is not this worker's orchestrator", workorders.ErrInvalid, e.Lane)
	}
	report := ledger.OrchestratorReport{Lane: e.Lane, Generation: e.Generation, Kind: e.Type}
	switch e.Type {
	case ledger.UploadLaneStatus:
		var status ledger.LaneStatus
		if err := json.Unmarshal(e.Payload, &status); err != nil {
			return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
		}
		report.Actual = status.ActualState
	case ledger.UploadLaneEnded:
		report.Actual = "ended"
	}
	if err := tx.Workers().SaveOrchestrator(row.Apply(report)); err != nil {
		return err
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{
		ID: id, Type: ledger.EventOrchestratorReported, OccurredAt: e.OccurredAt, Data: data,
		Actor:   ledger.Actor{Kind: ledger.ActorWorker, ID: worker},
		Subject: ledger.Subject{Kind: ledger.SubjectOrchestrator, ID: row.ID},
	})
	return err
}

// WakeDigest reads what this worker's orchestrator could judge now. It reads only what the worker may
// act on, and a digest with nothing in it is what keeps an idle fleet from spending anything.
func (w *Workers) WakeDigest(ctx context.Context, request ledger.WakeRequest) (ledger.WakeDigest, error) {
	worker := request.Auth.WorkerID
	digest := ledger.WakeDigest{Items: []ledger.WakeItem{}}
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		if err := w.checkSession(tx, request.Auth, request.Epoch); err != nil {
			return err
		}
		for _, gather := range []func(ports.ReadTx, string) ([]ledger.WakeItem, error){wakeWork, wakeQuestions, wakeFinished, wakeLanes, wakeApprovals} {
			items, err := gather(tx, worker)
			if err != nil {
				return err
			}
			digest.Items = append(digest.Items, items...)
		}
		return nil
	})
	return digest, err
}

// wakeWork is claimable work, while this worker has room for it. A fleet that takes no claims has
// none, and a wake it could only decline would still read a whole context.
func wakeWork(tx ports.ReadTx, worker string) ([]ledger.WakeItem, error) {
	workspace, err := tx.Workspace()
	if err != nil {
		return nil, err
	}
	if workspace.FleetMode != ledger.FleetRun {
		return nil, nil
	}
	record, err := tx.Workers().Find(worker)
	if err != nil {
		return nil, err
	}
	held, err := tx.WorkOrders().HeldBy(worker)
	if err != nil {
		return nil, err
	}
	if capacity := record.Manifest.Capacity.MaxLanes; capacity != 0 && int64(len(held)) >= capacity {
		return nil, nil
	}
	rows, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	var out []ledger.WakeItem
	for _, row := range rows {
		if row.State != workorders.Ready {
			continue
		}
		offered, err := offers(tx, row, worker)
		if err != nil {
			return nil, err
		}
		if offered {
			out = append(out, ledger.WakeItem{Kind: ledger.WakeWork, ID: row.ID, Stamp: row.Rev, Text: row.Title})
		}
	}
	return out, nil
}

// wakeQuestions is what waits on the ladder and nobody holds. A question past its gate waits on a
// person, and no wake would change that.
func wakeQuestions(tx ports.ReadTx, _ string) ([]ledger.WakeItem, error) {
	rows, err := freeQuestions(tx, "")
	if err != nil {
		return nil, err
	}
	out := make([]ledger.WakeItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, ledger.WakeItem{Kind: ledger.WakeQuestion, ID: row.ID, Stamp: row.ClaimUntil + row.Rung, Text: row.Text})
	}
	return out, nil
}

// questionPage is how many questions one read takes at a time.
const questionPage = 200

// freeQuestions walks every question that waits on the ladder and nobody else holds. A digest and the
// tool that takes the next read this one list, so what a wake names is what a session can take.
func freeQuestions(tx ports.ReadTx, holder string) ([]questions.Question, error) {
	now, err := tx.Now()
	if err != nil {
		return nil, err
	}
	var out []questions.Question
	for after := ""; ; {
		rows, err := tx.Questions().Questions(ports.RecordFilter{After: after, Limit: questionPage})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			after = row.ID
			waits := row.Status == questions.StatusOpen || row.Status == questions.StatusTriage
			if waits && (row.ClaimUntil <= now || (holder != "" && row.ClaimedBy == holder)) {
				out = append(out, row)
			}
		}
		if len(rows) < questionPage {
			return out, nil
		}
	}
}

// wakeFinished is a lane that ended and left its work order for review.
func wakeFinished(tx ports.ReadTx, _ string) ([]ledger.WakeItem, error) {
	rows, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	var out []ledger.WakeItem
	for _, row := range rows {
		if row.State == workorders.Acceptance {
			out = append(out, ledger.WakeItem{Kind: ledger.WakeFinished, ID: row.ID, Stamp: row.Rev, Text: row.Title})
		}
	}
	return out, nil
}

// wakeLanes is what `stalled` and `overdue` exist to say: a person or an orchestrator has to read it,
// because neither condition ends a lane by itself. Each counts once for as long as it stands.
func wakeLanes(tx ports.ReadTx, worker string) ([]ledger.WakeItem, error) {
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return nil, err
	}
	var out []ledger.WakeItem
	for _, row := range rows {
		if row.WorkerID != worker {
			continue
		}
		for _, condition := range row.Status.Conditions {
			switch condition {
			case "stalled":
				out = append(out, ledger.WakeItem{Kind: ledger.WakeStalled, ID: row.ID, Stamp: row.Generation, Text: "lane " + row.ID + " on " + row.WorkOrder + " shows no progress"})
			case "overdue":
				out = append(out, ledger.WakeItem{Kind: ledger.WakeOverdue, ID: row.ID, Stamp: row.Generation, Text: "lane " + row.ID + " on " + row.WorkOrder + " is past its deadline"})
			}
		}
	}
	return out, nil
}

// wakeApprovals is how an orchestrator reads a request: only what a member delegated reaches it, with
// the digest a decision binds to, and the summary a reviewer reads. Its own requests go to a member.
func wakeApprovals(tx ports.ReadTx, worker string) ([]ledger.WakeItem, error) {
	version, err := tx.Policies().InForce(authority.PolicyDelegation)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	policy, err := authority.ReadDelegation(version)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Approvals().All()
	if err != nil {
		return nil, err
	}
	self := authority.Principal(ledger.Actor{Kind: ledger.ActorOrchestrator, ID: worker})
	var out []ledger.WakeItem
	for _, row := range rows {
		if !row.Open() || row.RequestedBy == self || !policy.Covers(row) {
			continue
		}
		out = append(out, ledger.WakeItem{
			Kind: ledger.WakeApproval, ID: row.ID, Stamp: row.RaisedAt, Digest: row.SubjectDigest,
			Text: row.Subject + ": " + row.Summary,
		})
	}
	return out, nil
}
