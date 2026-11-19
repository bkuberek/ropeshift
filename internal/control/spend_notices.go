package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// spendNoticeWindow is how long a spend notice is still offered to the channels that have not heard it. A channel that was
// down for a day is asked again when it comes back, and what is older than the window is still in the ledger and the web
// UI, which show every notice, and asks no channel any more.
const spendNoticeWindow = 7 * 24 * time.Hour

// spendActor is who the ledger names as the source of an alert no member and no worker raised.
var spendActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "spend"}

// recordSpendNotice records that a spend notice exists, in the transaction of what caused it, so an alert and its cause are
// one commit. A soft or hard limit is told once for its kind, scope, subject, window, and limit, so a limit that is reported
// again says nothing again (FR-SPEND-19). The other kinds are each an occurrence, and the producer of one says when.
func recordSpendNotice(tx ports.WriteTx, n ledger.SpendNotice, actor ledger.Actor) error {
	draft, err := ledger.SpendNoticeDraft(n, actor)
	if err != nil {
		return err
	}
	if n.Kind == ledger.NoticeSpendSoftLimit || n.Kind == ledger.NoticeSpendHardLimit {
		told, err := tx.EventsAbout(draft.Subject)
		if err != nil {
			return err
		}
		for _, event := range told {
			if earlier, err := ledger.ReadSpendNotice(event); err == nil && earlier.Key() == n.Key() {
				return nil
			}
		}
	}
	_, err = tx.Append(draft)
	return err
}

// Note records a spend notice no limit of this build raises: the no-progress alert of a lane that parked, and the alert that
// a lane's spend is mostly waiting, whose workers come with the slices that detect them. They call this, and a test calls it
// to prove the kinds are delivered the same way. Nothing a member or an agent can reach calls it.
func (s *Spend) Note(ctx context.Context, scope ledger.WorkspaceID, n ledger.SpendNotice, actor ledger.Actor) error {
	return s.store.Write(ctx, scope, "spend.notice", func(tx ports.WriteTx) error { return recordSpendNotice(tx, n, actor) })
}

// SpendWaiting lists the spend notices the channels may still have to hear, as the notices a tick delivers: one for each
// event of a spend notice in the window, in the words of the notice table and with the project named, so a webhook and a
// desktop say only what the table does. A notice a channel heard is not in the list's retry, because the ledger's record of
// the delivery says so, as it does for everything else a tick sends.
func (n *Notices) SpendWaiting(ctx context.Context, scope ledger.WorkspaceID, project string) ([]ledger.Notice, error) {
	var out []ledger.Notice
	err := n.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectSpendNotice, ID: ledger.SpendNoticeSubject})
		if err != nil {
			return err
		}
		for _, event := range events {
			if now-event.RecordedAt > spendNoticeWindow.Milliseconds() {
				continue
			}
			notice, err := ledger.ReadSpendNotice(event)
			if err != nil {
				return err
			}
			out = append(out, ledger.NewNotice(scope, notice.Kind, notice.Scope, event.ID, event.RecordedAt).In(project))
		}
		return nil
	})
	return out, err
}

// assistantSpent keeps what an assistant's run says a generation has spent, in the tally of the assistant's day, and tells the
// project once for the day when it reaches `assistant_daily_warn` (FR-SPEND-6). The assistant has a soft daily limit and no
// hard one: stopping it would stop the question ladder, so nothing here holds it back.
func assistantSpent(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var usage ledger.GenerationUsage
	if err := json.Unmarshal(e.Payload, &usage); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if usage.CostUSD < 0 || usage.KnownTurns < 0 || usage.UnknownTurns < 0 {
		return fmt.Errorf("%w: a generation's usage is negative", workorders.ErrInvalid)
	}
	usage.FloorUSD = 0
	e.Payload = nil
	draft, err := uploadedDraft(tx, worker, id, e)
	if err != nil {
		return err
	}
	if _, err := tx.Append(draft); err != nil {
		return err
	}
	if err := tx.Lanes().NoteSpend(ledger.SpendScopeAssistant, e.Lane, e.Generation, usage); err != nil {
		return err
	}
	reads := projectWritesOf(tx)
	policy, err := spendPolicyInForce(reads.policies)
	if err != nil || policy.AssistantDailyWarn <= 0 {
		return err
	}
	now, err := reads.now()
	if err != nil {
		return err
	}
	start := ledger.WindowStart(ledger.WindowDay, now)
	day, err := tx.Lanes().SpendSince(ledger.SpendScopeAssistant, start)
	if err != nil || day < policy.AssistantDailyWarn {
		return err
	}
	return recordSpendNotice(tx, ledger.SpendNotice{
		Kind: ledger.NoticeSpendSoftLimit, Scope: ledger.NoticeScopeAssistant, WindowStart: start, LimitUSD: policy.AssistantDailyWarn,
		ChargedUSD: day, PolicyVersion: policy.Version,
	}, spendActor)
}

// orchestratorSpent keeps what an orchestrator's generation says it has spent, in the tally of its machine's day, which
// `orchestrator_daily_warn` reads (FR-SPEND-6). The orchestrator has a soft daily limit and no hard one: stopping it would
// stop recovery and the question ladder, so nothing here holds it back.
func orchestratorSpent(tx ports.SharedTx, worker string, e ledger.UploadEvent) error {
	var usage ledger.GenerationUsage
	if err := json.Unmarshal(e.Payload, &usage); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if usage.CostUSD < 0 || usage.KnownTurns < 0 || usage.UnknownTurns < 0 {
		return fmt.Errorf("%w: a generation's usage is negative", workorders.ErrInvalid)
	}
	usage.FloorUSD = 0
	return tx.Spend().NoteSpend(ledger.OrchestratorSpendScope(worker), e.Lane, e.Generation, usage)
}

// warnOrchestrator tells every project the worker serves, once for the UTC day, that its machine's orchestrator reached
// `orchestrator_daily_warn`, so each project's `[notify_policy]` picks the channels. A day is a machine's: two machines have
// two tallies and two notices. It reads the tally and not the report that was stored, so a report sent again after a
// failure tells what the first one did not, and a notice already told says nothing again.
//
// start is the UTC day the report counted in, which the transaction that stored it chose, so a notice for a day that ended
// before this check is still told for that day. A project that is archived or gone is skipped, and every other project is
// tried before the first error that is worth sending the report again for is returned.
func (p *Plane) warnOrchestrator(ctx context.Context, worker string, start int64) error {
	notice, err := orchestratorWarning(ctx, p.installation, worker, start)
	if err != nil || notice == nil {
		return err
	}
	projects, err := p.serving(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, project := range projects {
		w, err := p.workers(ctx, project)
		if err == nil {
			err = w.noteOrchestratorWarning(ctx, *notice)
		}
		if errors.Is(err, ports.ErrNotFound) || errors.Is(err, ledger.ErrProjectArchived) {
			continue
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// orchestratorWarning reads what a machine's orchestrator was charged on one UTC day against `orchestrator_daily_warn`, and
// returns the notice the day owes, or nil when no warning is set or the day is under it.
func orchestratorWarning(ctx context.Context, installation ports.Installation, worker string, start int64) (*ledger.SpendNotice, error) {
	var policy authority.InstallationSpendPolicy
	var day float64
	err := installation.Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		if policy, err = installationSpendInForce(tx.Policies()); err != nil || policy.OrchestratorDailyWarn <= 0 {
			return err
		}
		day, err = tx.Spend().SpendOn(ledger.OrchestratorSpendScope(worker), start)
		return err
	})
	if err != nil || policy.OrchestratorDailyWarn <= 0 || day < policy.OrchestratorDailyWarn {
		return nil, err
	}
	return &ledger.SpendNotice{
		Kind: ledger.NoticeSpendSoftLimit, Scope: ledger.NoticeScopeOrchestrator, Subject: worker, WindowStart: start,
		LimitUSD: policy.OrchestratorDailyWarn, ChargedUSD: day, PolicyVersion: policy.Version,
	}, nil
}

// warnOrchestrator tells this project alone, for a plane of one project, as Plane.warnOrchestrator tells each of its own.
func (w *Workers) warnOrchestrator(ctx context.Context, worker string, start int64) error {
	notice, err := orchestratorWarning(ctx, w.store.Installation(w.scope), worker, start)
	if err != nil || notice == nil {
		return err
	}
	return w.noteOrchestratorWarning(ctx, *notice)
}

// noteOrchestratorWarning records an orchestrator's warning in this project's ledger, where it is told once for the day.
func (w *Workers) noteOrchestratorWarning(ctx context.Context, n ledger.SpendNotice) error {
	return w.store.Write(ctx, w.scope, "spend.notice", func(tx ports.WriteTx) error { return recordSpendNotice(tx, n, spendActor) })
}

// dollarsText says an amount the way a notice records a limit before and after.
func dollarsText(amount float64) string { return strconv.FormatFloat(amount, 'g', -1, 64) }

// noteLimitRaised records that a member raised a limit, with the limit before and after and who raised it, in the
// transaction of the raise (FR-SPEND-19).
func noteLimitRaised(tx ports.WriteTx, subject, key string, before, after float64, version int64, actor ledger.Actor) error {
	return recordSpendNotice(tx, ledger.SpendNotice{
		Kind: ledger.NoticeSpendPolicyChanged, Scope: ledger.NoticeScopeLimitRaised, Subject: subject, By: actor.ID, PolicyVersion: version,
		Changes: []ledger.SpendKeyChange{{Key: key, Before: dollarsText(before), After: dollarsText(after)}},
	}, actor)
}
