package worker_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A member's stop of an orchestrator that waits for its first wake ends the run as fenced, as a stop of one with a
// session does. The worker did not restart, so no run reads worker_restarted.
func TestAMembersStopOfAnOrchestratorAwaitingItsFirstWakeEndsItFenced(t *testing.T) {
	t.Parallel()
	o := newOrchestratorRig(t, aSession())
	o.start(t)
	o.tick(t)
	o.tick(t)
	if len(o.specs()) != 0 {
		t.Fatalf("a session started with nothing to judge: %+v", o.specs())
	}
	if _, err := o.plane.SetOrchestrator(t.Context(), o.subject.Auth().WorkerID, ledger.OrchestratorStopped, member); err != nil {
		t.Fatal(err)
	}
	o.await(t, "an end", func(row ledger.Orchestrator) bool { return row.Actual == "ended" })
	if row := o.row(t); row.EndReason != workorders.ReasonFenced {
		t.Errorf("the run ended with %q, want %q", row.EndReason, workorders.ReasonFenced)
	}
}
