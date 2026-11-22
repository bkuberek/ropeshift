package worker_test

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A drain stops what lanes start and lets a crashed lane end, and it leaves an orchestrator as it is, because an
// orchestrator holds no lane's place. A restart that finds the fleet draining continues a waiting orchestrator the
// same way: it waits for something to judge, and starts no session before then.
func TestARestartInADrainingFleetLeavesAWaitingOrchestrator(t *testing.T) {
	o := newOrchestratorRig(t, aSession([]fake.DriverStep{{Hold: true}}))
	row := o.start(t)
	o.tick(t)
	if got := len(o.specs()); got != 0 {
		t.Fatalf("%d sessions before anything to judge", got)
	}
	if err := control.NewFleet(o.store).SetMode(t.Context(), scope, ledger.FleetDrain, member); err != nil {
		t.Fatal(err)
	}
	actions := o.restarted(t)
	if !slices.Contains(actions, lanes.Action{Kind: lanes.ActionContinue, Lane: row.Lane()}) {
		t.Fatalf("the restart did %+v, and no session was there to adopt", actions)
	}
	for range 3 {
		o.tick(t)
	}
	record, err := o.journal.Lane(t.Context(), row.Lane())
	if err != nil {
		t.Fatal(err)
	}
	if record.State != lanes.StateRunning {
		t.Errorf("a restart in a draining fleet left the orchestrator %s", record.State)
	}
	if got := len(o.specs()); got != 0 {
		t.Errorf("a restart in a draining fleet started %d sessions", got)
	}
}
