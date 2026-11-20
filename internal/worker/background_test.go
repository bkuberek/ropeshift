package worker_test

import (
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// stopHook is a session's Stop hook, with the background work it says still runs.
func (r *laneRig) stopHook(t *testing.T, background int) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: lanes.EventStop, Subject: "session-1", SessionID: "session-1", Background: background, AtMs: time.Now().UnixMilli()})
}

// restsOnBackground waits for the lane to rest on the background work its last turn left running.
func (r *laneRig) restsOnBackground() {
	r.t.Helper()
	r.eventually("a lane at rest on its background work", func(view ledger.LaneView) bool {
		return view.ActualState == string(lanes.Idle) && slices.Contains(view.Conditions, string(lanes.WaitingExternal))
	})
}

// A lane whose turn ends while its own background agents still run has not finished: the worker neither
// nudges it nor ends it, and the turn their reports start is the one that ends it. A Stop hook says which
// session is paused on background work (https://code.claude.com/docs/en/hooks#stop-input), and a background
// subagent's report starts that session's next turn (https://code.claude.com/docs/en/hooks#userpromptsubmit).
func TestALaneWhoseTurnEndsOnBackgroundWorkWaitsForIt(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped, reported := newGate(), newGate()
	r.gates(stopped, reported)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, stopped.step(), completed(0.01), reported.step(), r.reports(workorders.OutcomeCompleted), completed(0.02),
	}))
	r.atGate(stopped)
	r.stopHook(t, 4)
	stopped.free()
	r.reach(t, reported)
	r.restsOnBackground()
	r.stopHook(t, 0)
	reported.free()
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("sent %+v to a lane whose agents were still at work", sent)
	}
}

// Once no background work runs, a turn that ends with no outcome is nudged as any other.
func TestALaneIsNudgedOnceItsBackgroundWorkEnds(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped, returned := newGate(), newGate()
	r.gates(stopped, returned)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01), returned.step(), completed(0.02)},
		[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.03)},
	))
	r.atGate(stopped)
	r.stopHook(t, 2)
	stopped.free()
	r.reach(t, returned)
	r.restsOnBackground()
	r.stopHook(t, 0)
	returned.free()
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := r.driver.Sent(); len(sent) != 1 || sent[0].Text != lanes.Nudge {
		t.Errorf("sent %+v, want the one nudge", sent)
	}
}

// A lane that reported has said its work is over, so it stops at the end of its turn whatever still runs.
func TestALaneThatReportedStopsWithBackgroundWorkInFlight(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped := newGate()
	r.gates(stopped)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), stopped.step(), completed(0.01)}))
	r.atGate(stopped)
	r.stopHook(t, 3)
	stopped.free()
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("sent %+v to a lane that reported", sent)
	}
}
