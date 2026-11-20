package worker_test

import (
	"slices"
	"strings"
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

// promptHook is a session's UserPromptSubmit hook, which a background subagent's report fires too
// (https://code.claude.com/docs/en/hooks#userpromptsubmit).
func (r *laneRig) promptHook(t *testing.T) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "session-1", SessionID: "session-1", AtMs: time.Now().UnixMilli()})
}

// restsOnBackground waits for the lane to rest on the background work its last turn left running, under the
// condition that names that wait.
func (r *laneRig) restsOnBackground() {
	r.t.Helper()
	r.eventually("a lane at rest on its background work", func(view ledger.LaneView) bool {
		return view.ActualState == string(lanes.Idle) && slices.Contains(view.Conditions, string(lanes.WaitingBackground))
	})
}

// sentText is what the worker sent the lane, in order.
func (r *laneRig) sentText() []string {
	var out []string
	for _, input := range r.driver.Sent() {
		out = append(out, input.Text)
	}
	return out
}

// awaitInputs waits for the worker to have sent the lane n inputs, moving the worker's clock a second at a time, so a
// rest on that clock ends however many reads it takes.
func (r *laneRig) awaitInputs(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for len(r.driver.Sent()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("sent %q, want %d inputs", r.sentText(), n)
		}
		r.clock.Advance(time.Second)
		time.Sleep(5 * time.Millisecond)
	}
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
	r.promptHook(t)
	reported.free()
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("sent %+v to a lane whose agents were still at work", sent)
	}
}

// A background subagent's report starts a turn with UserPromptSubmit, which ends the count its last Stop left: the
// turn it starts ends as any other, and one that ends with no outcome is nudged.
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
	r.promptHook(t)
	returned.free()
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := r.sentText(); len(sent) != 1 || sent[0] != lanes.Nudge {
		t.Errorf("sent %q, want the one nudge", sent)
	}
}

// A lane rests on its background work for as long as it may wait live (max_live_wait) and no longer: then it is
// nudged with words that name the work it waits on.
func TestARestOnBackgroundWorkIsBoundedByTheLiveWait(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped := newGate()
	r.gates(stopped)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)},
		[]fake.DriverStep{r.reports(workorders.OutcomeFailed), completed(0.02)},
	))
	r.atGate(stopped)
	r.stopHook(t, 1)
	stopped.free()
	r.restsOnBackground()
	r.clock.Advance(lanes.MaxLiveWait(nil) - time.Minute)
	time.Sleep(10 * superviseStep)
	if sent := r.sentText(); len(sent) != 0 {
		t.Fatalf("sent %q inside the live wait", sent)
	}
	r.awaitInputs(t, 1)
	if sent := r.sentText(); sent[0] != lanes.BackgroundNudge {
		t.Errorf("sent %q, want the nudge that names the background work", sent)
	}
	r.awaitOrderState(t, workorders.Failed)
}

// A lane whose nudge is spent and that rests on its background work past the live wait again ends blocked, with a
// reason that names the wait and a detail that says since when and what ended it, so it reaches Needs you.
func TestALaneThatOutwaitsItsNudgeOnBackgroundWorkIsBlockedWithItsReason(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped, again := newGate(), newGate()
	r.gates(stopped, again)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)},
		[]fake.DriverStep{again.step(), completed(0.02)},
	))
	r.atGate(stopped)
	r.stopHook(t, 2)
	stopped.free()
	r.restsOnBackground()
	r.awaitInputs(t, 1)
	r.reach(t, again)
	r.promptHook(t)
	r.stopHook(t, 3)
	again.free()
	r.restsOnBackground()
	deadline := time.Now().Add(20 * time.Second)
	for r.row().State == workorders.Running && time.Now().Before(deadline) {
		r.clock.Advance(time.Second)
		time.Sleep(5 * time.Millisecond)
	}
	r.awaitOrderState(t, workorders.Blocked)
	change := r.outcome()
	if change.Reason != workorders.ReasonBackgroundUnreported {
		t.Errorf("outcome %+v, want %s", change, workorders.ReasonBackgroundUnreported)
	}
	for _, want := range []string{"3 background tasks", "max_live_wait"} {
		if !strings.Contains(change.Detail, want) {
			t.Errorf("detail %q leaves out %q", change.Detail, want)
		}
	}
	if sent := r.sentText(); len(sent) != 1 {
		t.Errorf("sent %q, want the one nudge", sent)
	}
}

// The background count holds back only the nudge: a planning lane whose turn ends with background work in flight
// still waits for its person, as every other check after a turn still runs.
func TestAPlanningLaneWithBackgroundWorkStillWaitsForItsPerson(t *testing.T) {
	t.Parallel()
	r := planningRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\n", func(driver *fake.AgentDriver) { driver.SetAttached(true) })
	stopped := newGate()
	r.gates(stopped)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)},
		[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.02)},
	))
	r.atGate(stopped)
	r.stopHook(t, 2)
	stopped.free()
	r.waitsForItsPerson(t)
	if err := r.driver.Type("lane-1", 1, "That is the plan. Report it."); err != nil {
		t.Fatal(err)
	}
	r.awaitOrder(t, workorders.Acceptance)
	if r.nudged() {
		t.Errorf("a planning lane waiting for its person was nudged: %+v", r.driver.Sent())
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
