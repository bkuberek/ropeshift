package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

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
	e.Payload = nil
	draft, err := uploadedDraft(tx, worker, id, e)
	if err != nil {
		return err
	}
	if _, err := tx.Append(draft); err != nil {
		return err
	}
	if err := tx.Lanes().NoteSpend(ledger.SpendScopeAssistant, e.Lane, e.Generation, ledger.Charge([][]ledger.GenerationUsage{{usage}}).ChargedUSD); err != nil {
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
