package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// Acceptances holds the step from `acceptance` to `done`, or back to `ready`. A lane cannot
// accept its own work, because that would be an agent approving its own request, so each
// step compares the contract's criteria with evidence the lane did not write by itself.
type Acceptances struct {
	store ports.Store
}

// NewAcceptances returns the use cases over a Store.
func NewAcceptances(store ports.Store) *Acceptances {
	return &Acceptances{store: store}
}

// Standing reads what a work order's evidence leaves to do, with everything linked to it.
func (a *Acceptances) Standing(ctx context.Context, scope ledger.WorkspaceID, id string) (workorders.AcceptanceView, error) {
	var view workorders.AcceptanceView
	err := a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		view, err = StandingIn(tx, id)
		return err
	})
	return view, err
}

// StandingIn reads where a work order stands inside a read, so a read model sees one moment.
func StandingIn(tx ports.ReadTx, id string) (workorders.AcceptanceView, error) {
	current, err := tx.WorkOrders().Get(id)
	if err != nil {
		return workorders.AcceptanceView{}, err
	}
	assessment, err := assessAttempt(tx.Planning(), tx.Evidence(), current)
	if err != nil {
		return workorders.AcceptanceView{}, err
	}
	artifacts, err := tx.Evidence().Artifacts(id)
	if err != nil {
		return workorders.AcceptanceView{}, err
	}
	checks, err := tx.Evidence().Checks(id)
	if err != nil {
		return workorders.AcceptanceView{}, err
	}
	return workorders.AcceptanceView{
		WorkOrder: id, State: current.State, Assessment: assessment,
		Artifacts: append([]ledger.Artifact{}, artifacts...), Checks: append([]workorders.Evidence{}, checks...),
	}, nil
}

// assessAttempt compares the pinned contract with the current attempt's evidence.
func assessAttempt(planned ports.PlanningReader, evidence ports.EvidenceReader, w workorders.WorkOrder) (workorders.Assessment, error) {
	var criteria []string
	if w.ContractVersion != 0 {
		contract, err := planned.Contract(w.ContractID, w.ContractVersion)
		if err != nil {
			return workorders.Assessment{}, err
		}
		for _, criterion := range contract.Criteria {
			criteria = append(criteria, criterion.ID)
		}
	}
	rows, err := evidence.Attempt(w.ID, w.Fence)
	if err != nil {
		return workorders.Assessment{}, err
	}
	return workorders.Assess(criteria, rows), nil
}

// acceptAction names what an assessment asks of whoever accepts.
func acceptAction(needs workorders.Need) authority.Action {
	switch needs {
	case workorders.NeedNothing:
		return authority.ActionAccept
	case workorders.NeedSecondPrincipal:
		return authority.ActionAcceptLaneChecks
	}
	return authority.ActionAcceptUnchecked
}

// Accept moves accepted work to `done`, once the caller may answer for what the evidence
// leaves open. A failed check cannot be accepted, whoever asks. A repeated acceptance changes
// nothing, and ropeshift does not merge: a branch artifact ends the work (D17).
func (a *Acceptances) Accept(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (workorders.State, error) {
	state := workorders.Done
	err := a.store.Write(ctx, scope, "acceptance.accept", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		assessment, err := assessAttempt(tx.Planning(), tx.Evidence(), current)
		if err != nil {
			return err
		}
		if !authority.Allowed(actor, acceptAction(assessment.Needs)) {
			return workorders.ErrForbidden
		}
		if current.State == workorders.Done {
			return nil
		}
		if len(assessment.Failed) != 0 {
			return fmt.Errorf("%w: %s", workorders.ErrNotAcceptable, strings.Join(assessment.Failed, ", "))
		}
		return accept(tx, id, assessment, actor, func() { crashpoint.Hit("acceptance.accept", "moved") })
	})
	if err != nil {
		return "", err
	}
	return state, nil
}

func accept(tx ports.WriteTx, id string, assessment workorders.Assessment, actor ledger.Actor, moved func()) error {
	payload, err := json.Marshal(assessment)
	if err != nil {
		return err
	}
	if err := leaveAcceptance(tx, id, workorders.Change{Trigger: workorders.TriggerAcceptancePassed, Payload: payload}, actor, moved); err != nil {
		return err
	}
	if err := passPlanReview(tx, id, actor); err != nil {
		return err
	}
	_, err = fireWakes(tx, "done:"+id, actor, parking.WorkOrderDone)
	return err
}

// leaveAcceptance takes one edge out of `acceptance`. The compare-and-set repeats the state
// read, so a racing move loses instead of moving the work twice. moved names the caller's
// crash step, which has to be a literal for the matrix to find it.
func leaveAcceptance(tx ports.WriteTx, id string, change workorders.Change, actor ledger.Actor, moved func()) error {
	current, err := tx.WorkOrders().Get(id)
	if err != nil {
		return err
	}
	edge, err := workorders.Move(current, change.Trigger)
	if err != nil {
		return err
	}
	won, err := tx.WorkOrders().Move(id, edge.From, edge.To)
	if err != nil {
		return err
	}
	if !won {
		return workorders.ErrIllegalMove
	}
	moved()
	event, err := workorders.ChangeEvent(workorders.EventType(change.Trigger), id, change, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// Rework sends completed work back for another attempt. The evidence of this one stays linked
// to the work order and stops counting, because the next lane's end moves the fence.
func (a *Acceptances) Rework(ctx context.Context, scope ledger.WorkspaceID, id, reason string, actor ledger.Actor) (workorders.State, error) {
	if !authority.Allowed(actor, authority.ActionRework) {
		return "", workorders.ErrForbidden
	}
	if strings.TrimSpace(reason) == "" {
		return "", fmt.Errorf("%w: a rework needs a reason", workorders.ErrInvalid)
	}
	var state workorders.State
	err := a.store.Write(ctx, scope, "acceptance.rework", func(tx ports.WriteTx) error {
		var err error
		state, err = rework(tx, id, reason, actor, func() { crashpoint.Hit("acceptance.rework", "moved") })
		return err
	})
	if err != nil {
		return "", err
	}
	return state, nil
}

// rework runs the definition of ready, as every move into `ready` does, so a question that
// opened during the attempt holds the work back in `contract_review`.
func rework(tx ports.WriteTx, id, reason string, actor ledger.Actor, moved func()) (workorders.State, error) {
	err := leaveAcceptance(tx, id, workorders.Change{Trigger: workorders.TriggerRework, Reason: reason}, actor, moved)
	if err != nil {
		return "", err
	}
	back, err := tx.WorkOrders().Get(id)
	if err != nil {
		return "", err
	}
	failures, err := readyFailures(tx.Planning(), tx.WorkOrders(), tx.Questions(), back)
	if err != nil {
		return "", err
	}
	if len(failures) == 0 {
		return workorders.Ready, nil
	}
	if err := takeEdge(tx, id, workorders.TriggerBackToReview, actor); err != nil {
		return "", err
	}
	return workorders.ContractReview, nil
}

// awaiting is a work order in `acceptance`, as a tick read it with no lock.
type awaiting struct {
	order  workorders.WorkOrder
	branch ledger.Artifact
}

// Settle reads what a forge ran on each waiting attempt's commit, and moves the work its
// evidence decides by itself. It is a tick: the list is read with no lock, the forge is read
// outside every transaction, and each item is one transaction that reads again. A nil forge
// means no independent source exists, which leaves a lane's checks to a second principal
// (D16). A forge that fails decides nothing about its item, and the rest go on.
func (a *Acceptances) Settle(ctx context.Context, scope ledger.WorkspaceID, forge ports.Forge, actor ledger.Actor) ([]string, error) {
	waiting, err := a.waiting(ctx, scope)
	if err != nil {
		return nil, err
	}
	var moved []string
	var failed []error
	for _, item := range waiting {
		checks, err := forgeChecks(ctx, scope, forge, item)
		if err != nil {
			failed = append(failed, fmt.Errorf("work order %s: %w", item.order.ID, err))
			continue
		}
		settled, err := a.settle(ctx, scope, item, checks, actor)
		if err != nil {
			failed = append(failed, fmt.Errorf("work order %s: %w", item.order.ID, err))
			continue
		}
		if settled {
			moved = append(moved, item.order.ID)
		}
	}
	return moved, errors.Join(failed...)
}

func (a *Acceptances) waiting(ctx context.Context, scope ledger.WorkspaceID) ([]awaiting, error) {
	var out []awaiting
	err := a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.State != workorders.Acceptance {
				continue
			}
			artifacts, err := tx.Evidence().Artifacts(row.ID)
			if err != nil {
				return err
			}
			out = append(out, awaiting{order: row, branch: attemptBranch(artifacts, row.Fence)})
		}
		return nil
	})
	return out, err
}

// attemptBranch finds the branch one attempt left. An attempt with none has no commit a forge
// could have run on.
func attemptBranch(artifacts []ledger.Artifact, fence int64) ledger.Artifact {
	for _, artifact := range artifacts {
		if artifact.Kind == ledger.ArtifactBranch && artifact.Fence == fence {
			return artifact
		}
	}
	return ledger.Artifact{}
}

func forgeChecks(ctx context.Context, scope ledger.WorkspaceID, forge ports.Forge, item awaiting) ([]ports.ForgeCheck, error) {
	if forge == nil || item.branch.Digest == "" {
		return nil, nil
	}
	return forge.Checks(ctx, scope, ports.ForgeRequest{Repository: item.order.RepositoryID, Commit: item.branch.Digest})
}

// settle decides one attempt. The fence is compared, because what the forge said is about the
// attempt the tick read, and a later one may have started since.
func (a *Acceptances) settle(ctx context.Context, scope ledger.WorkspaceID, item awaiting, checks []ports.ForgeCheck, actor ledger.Actor) (bool, error) {
	settled := false
	err := a.store.Write(ctx, scope, "acceptance.settle", func(tx ports.WriteTx) error {
		settled = false
		current, err := tx.WorkOrders().Get(item.order.ID)
		if err != nil {
			return err
		}
		if current.State != workorders.Acceptance || current.Fence != item.order.Fence {
			return nil
		}
		recorded, err := recordForgeChecks(tx, current, item.branch, checks)
		if err != nil {
			return err
		}
		crashpoint.Hit("acceptance.settle", "recorded")
		assessment, err := assessAttempt(tx.Planning(), tx.Evidence(), current)
		if err != nil {
			return err
		}
		moved := func() { crashpoint.Hit("acceptance.settle", "moved") }
		switch {
		case len(assessment.Failed) != 0:
			settled = true
			_, err = rework(tx, current.ID, "failed: "+strings.Join(assessment.Failed, ", "), actor, moved)
			return err
		case assessment.Needs == workorders.NeedNothing:
			if !authority.Allowed(actor, acceptAction(assessment.Needs)) {
				return workorders.ErrForbidden
			}
			settled = true
			return accept(tx, current.ID, assessment, actor, moved)
		case recorded:
			_, err = tx.Append(ledger.EventDraft{
				Type: workorders.EventEvidenceRecorded, Actor: actor, Subject: ledger.Subject{Kind: workorders.SubjectKind, ID: current.ID},
			})
		}
		return err
	})
	return settled, err
}

// recordForgeChecks keeps what a forge said about one attempt, and reports whether that added
// anything. Each check's ID is its name within the attempt, so a check run again replaces
// its own row and a tick that learned nothing writes nothing.
func recordForgeChecks(tx ports.WriteTx, w workorders.WorkOrder, branch ledger.Artifact, checks []ports.ForgeCheck) (bool, error) {
	existing, err := tx.Evidence().Attempt(w.ID, w.Fence)
	if err != nil {
		return false, err
	}
	recorded := false
	for _, check := range checks {
		id := fmt.Sprintf("forge:%s:%d:%s", w.ID, w.Fence, check.Name)
		held := slices.IndexFunc(existing, func(row workorders.Evidence) bool { return row.ID == id })
		if held >= 0 && existing[held].Status == check.Status && existing[held].DetailsURI == check.DetailsURI {
			continue
		}
		err := tx.Evidence().Record(workorders.Evidence{
			ID: id, WorkOrder: w.ID, Lane: branch.Lane, Fence: w.Fence, ArtifactID: branch.ID, Criterion: check.Criterion,
			Name: check.Name, Status: check.Status, Source: workorders.SourceForge, DetailsURI: check.DetailsURI,
		})
		if err != nil {
			return false, err
		}
		recorded = true
	}
	return recorded, nil
}

// recordEvidence keeps what a lane's end says, in the transaction that stores the end, so an
// outcome can never stand without the artifacts and checks it rests on (FR-LANE-2). Only the
// branch comes from the worker. A lane's checks hang from it as the artifact they prove.
func recordEvidence(tx ports.WriteTx, holder workorders.Holder, fence int64, result workorders.LaneResult) error {
	addArtifact := func(kind, uri, digest string) (string, error) {
		id, err := newID(tx.Now())
		if err != nil {
			return "", err
		}
		return id, tx.Lanes().AddArtifact(ledger.Artifact{ID: id, Lane: holder.Lane, Kind: kind, URI: uri, Digest: digest, Fence: fence})
	}
	var branch string
	if result.Branch != "" {
		var err error
		if branch, err = addArtifact(ledger.ArtifactBranch, result.Branch, result.Head); err != nil {
			return err
		}
	}
	for _, uri := range result.Artifacts {
		if _, err := addArtifact(ledger.ArtifactReport, uri, ""); err != nil {
			return err
		}
	}
	for _, check := range result.Checks {
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		status := workorders.CheckFailed
		if check.Passed {
			status = workorders.CheckPassed
		}
		err = tx.Evidence().Record(workorders.Evidence{
			ID: id, WorkOrder: holder.WorkOrder, Lane: holder.Lane, Fence: fence, ArtifactID: branch, Criterion: check.Criterion,
			Name: check.Name, Status: status, Source: workorders.SourceLane,
		})
		if err != nil {
			return err
		}
	}
	return nil
}
