//go:build chaos

package chaos

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A failed work order stays failed until someone retries it, so the drain, which waits for every work
// order to be ready, retries it the way a member would.
func TestTheDrainRetriesFailedWork(t *testing.T) {
	w := newWorld(t, "")
	run := newRandomRun(w, 0)
	w.rig.MaxAttempts = 1
	id, err := w.rig.Ready(w.store)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := w.rig.Running(w.store, id, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	worker := ledger.Actor{Kind: ledger.ActorWorker, ID: "worker-1"}
	failed := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeFailed, Reason: workorders.ReasonCrashed}}
	if err := w.rig.NewOps(w.store).EndLane(w.rig.Ctx, w.rig.Scope, holder, failed, worker); err != nil {
		t.Fatal(err)
	}
	if got, err := w.rig.WorkOrder(w.store, id); err != nil || got.State != workorders.Failed {
		t.Fatalf("before the drain: %+v, err = %v", got, err)
	}
	if err := run.drain(); err != nil {
		t.Fatal(err)
	}
	if got, err := w.rig.WorkOrder(w.store, id); err != nil || got.State != workorders.Ready {
		t.Fatalf("after the drain: %+v, err = %v", got, err)
	}
}
