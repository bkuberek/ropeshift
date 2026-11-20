package control_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// failedWork records work order id as failed by its lane, which reported why, the way a lane's end records it.
func (r *planeRig) failedWork(id, reason string) {
	r.t.Helper()
	r.stuckWork(id, workorders.Failed, workorders.Change{Trigger: workorders.TriggerLaneFailed, Lane: "lane-" + id, Generation: 1, Outcome: workorders.OutcomeFailed, Reason: reason})
}

// stuckWork records work order id in state, with each change as an event, in order: the lane's it names, or the
// system's when it names none.
func (r *planeRig) stuckWork(id string, state workorders.State, changes ...workorders.Change) {
	r.t.Helper()
	err := r.stores["pa"].Write(r.t.Context(), "pa", "test.stuck_work", func(tx ports.WriteTx) error {
		if err := tx.WorkOrders().Insert(workorders.WorkOrder{ID: id, Kind: workorders.KindChange, State: state, Title: "T", Attempts: 1}); err != nil {
			return err
		}
		for _, change := range changes {
			actor := ledger.Actor{Kind: ledger.ActorSystem, ID: "spend"}
			if change.Lane != "" {
				actor = ledger.Actor{Kind: ledger.ActorLane, ID: change.Lane}
			}
			event, err := workorders.ChangeEvent(workorders.EventType(change.Trigger), id, change, actor)
			if err != nil {
				return err
			}
			if _, err := tx.Append(event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

// stuckWake reads the one wake item of kind for work order id.
func (r *planeRig) stuckWake(kind, id string) ledger.WakeItem {
	r.t.Helper()
	r.orchestrating()
	digest, err := r.plane.WakeDigest(r.t.Context(), ledger.WakeRequest{Auth: r.auth, Epoch: r.epoch})
	if err != nil {
		r.t.Fatal(err)
	}
	for _, item := range digest.Items {
		if item.Kind == kind && item.ID == id {
			return item
		}
	}
	r.t.Fatalf("the wake holds %+v, want %s %s", digest.Items, kind, id)
	return ledger.WakeItem{}
}

// A lane writes its report, so a wake states it as text a stranger wrote: with no terminal escape, and with each
// credential's shape redacted before the cut, so a cut never leaves part of one (ADR-0037).
func TestAWakeStatesALanesReportSanitized(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	r.failedWork("wo-a", "\x1b[31mThe push failed\x1b[0m with ghp_abcdefghijklmnopqrstuvwxyz0123456789 in the remote URL.")
	text := r.stuckWake(ledger.WakeFailed, "wo-a").Text
	if strings.Contains(text, "\x1b") || strings.Contains(text, "ghp_") || !strings.Contains(text, "The push failed") {
		t.Errorf("the wake says %q", text)
	}
}

// A wake quotes the report of the last lane that ended the work, and none when that lane did not fail or block it: a
// block a spend stop made after a later lane is not what an earlier lane reported.
func TestAWakeQuotesOnlyTheLastLanesReport(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	r.stuckWork("wo-a", workorders.Blocked,
		workorders.Change{Trigger: workorders.TriggerLaneBlocked, Lane: "lane-1", Generation: 1, Outcome: workorders.OutcomeBlocked, Reason: "The schema was undecided."},
		workorders.Change{Trigger: workorders.TriggerLaneCompleted, Lane: "lane-2", Generation: 1, Outcome: workorders.OutcomeCompleted, Reason: "Implemented."},
		workorders.Change{Trigger: workorders.TriggerSpendBlocked, Reason: "The project's daily limit was reached."},
	)
	if text := r.stuckWake(ledger.WakeBlocked, "wo-a").Text; strings.Contains(text, "schema was undecided") || strings.Contains(text, "reported") {
		t.Errorf("the wake says %q, which quotes a lane that did not block the work", text)
	}
}

// TestFailedWorkWakesTheOrchestratorAtEveryLevel holds that recovering failed work is no claim, so it wakes the
// orchestrator whatever the worker's level, and the wake carries what the lane reported, which a retry's reason can
// state (ADR-0014).
func TestFailedWorkWakesTheOrchestratorAtEveryLevel(t *testing.T) {
	for _, level := range []authority.Level{authority.LevelManual, authority.LevelAttended, authority.LevelUnattended} {
		t.Run(string(level), func(t *testing.T) {
			r := newPlaneRig(t, ledger.ProtocolVersion)
			if err := control.NewAutonomy(r.stores["pa"]).SetLevel(t.Context(), "pa", "worker-1", level, projectMember); err != nil {
				t.Fatal(err)
			}
			r.failedWork("wo-a", "The migration test needs a database the lane could not start.")
			r.orchestrating()
			digest, err := r.plane.WakeDigest(t.Context(), ledger.WakeRequest{Auth: r.auth, Epoch: r.epoch})
			if err != nil {
				t.Fatal(err)
			}
			var failed []ledger.WakeItem
			for _, item := range digest.Items {
				if item.Kind == ledger.WakeFailed {
					failed = append(failed, item)
				}
			}
			if len(failed) != 1 || failed[0].ID != "wo-a" {
				t.Fatalf("the wake holds %+v, want the failed work", digest.Items)
			}
			if !strings.Contains(failed[0].Text, "could not start") {
				t.Errorf("the wake says %q, which does not carry what the lane reported", failed[0].Text)
			}
		})
	}
}

// TestAManualWorkersOrchestratorRetriesFailedWork holds that a retry is no claim: it returns failed work to ready,
// where a member of a manual worker claims it, so an orchestrator retries at every level.
func TestAManualWorkersOrchestratorRetriesFailedWork(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	if err := control.NewAutonomy(r.stores["pa"]).SetLevel(t.Context(), "pa", "worker-1", authority.LevelManual, projectMember); err != nil {
		t.Fatal(err)
	}
	r.failedWork("wo-a", "The network was down.")
	call := r.orchestrating()
	call.Project, call.WorkOrder, call.Reason = "pa", "wo-a", "The lane reported the network was down, and it is back."
	if _, err := r.plane.Orchestrate(t.Context(), ledger.VerbRetry, call); err != nil {
		t.Fatalf("a retry at manual: %v", err)
	}
	call.Reason = "A restart is a lane's next generation, which a manual worker leaves to a member."
	if _, err := r.plane.Orchestrate(t.Context(), ledger.VerbRestart, call); !errors.Is(err, workorders.ErrIneligible) || !strings.Contains(err.Error(), "is manual") {
		t.Errorf("a lane restart at manual answered %v, want the worker's level refused", err)
	}
}
