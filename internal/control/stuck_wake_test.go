package control_test

import (
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
	lane := ledger.Actor{Kind: ledger.ActorLane, ID: "lane-" + id}
	err := r.stores["pa"].Write(r.t.Context(), "pa", "test.failed_work", func(tx ports.WriteTx) error {
		if err := tx.WorkOrders().Insert(workorders.WorkOrder{ID: id, Kind: workorders.KindChange, State: workorders.Failed, Title: "T", Attempts: 1}); err != nil {
			return err
		}
		change := workorders.Change{Trigger: workorders.TriggerLaneFailed, Lane: lane.ID, Generation: 1, Outcome: workorders.OutcomeFailed, Reason: reason}
		event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerLaneFailed), id, change, lane)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
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
	if _, err := r.plane.Orchestrate(t.Context(), ledger.VerbRestart, call); err == nil {
		t.Error("a lane restart at manual was taken")
	}
}
