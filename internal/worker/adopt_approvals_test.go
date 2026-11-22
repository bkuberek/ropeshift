package worker_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A restarted worker tells the driver which calls its lane still waits on, because the driver cannot
// read that from the hooks a journal kept.
func TestAnAdoptedSessionIsToldTheCallsItWaitsOn(t *testing.T) {
	t.Parallel()
	g := newGate()
	r := laneRigWith(t, approvalProfile)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), completed(0.01)}))
	r.start()
	r.reach(t, g)
	defer close(g.release)
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	r.look(t, gatedCall, "call-1", false)
	r.eventually("a lane that waits", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.NeedsDecision) })
	driver := r.takeOver(t, script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
	if approvals := driver.Adopted()[0].Approvals; len(approvals) != 1 || approvals[0] == "" {
		t.Errorf("the adopted session was told it waits on %q", approvals)
	}
}
