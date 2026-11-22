package worker_test

import (
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// stopAt is a session's Stop hook at a time, with the background work it says still runs.
func (r *laneRig) stopAt(t *testing.T, atMs int64, background int) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: lanes.EventStop, Subject: "session-1", SessionID: "session-1", Background: background, AtMs: atMs})
}

// failureAt is a session's StopFailure hook at a time.
func (r *laneRig) failureAt(t *testing.T, atMs int64, category string) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: "StopFailure", Subject: "session-1", SessionID: "session-1", Detail: category, AtMs: atMs})
}

// promptAt is a session's UserPromptSubmit hook at a time: a line a person typed, or the report of background work.
func (r *laneRig) promptAt(t *testing.T, atMs int64) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "session-1", SessionID: "session-1", AtMs: atMs})
}

// callAt is a tool call's start or end, by its hook, at a time.
func (r *laneRig) callAt(t *testing.T, event, subject string, atMs int64) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: event, Subject: subject, Tool: "Bash", AtMs: atMs})
}

// settle lets the worker's goroutines read a clock a test moved.
func settle() { time.Sleep(10 * superviseStep) }

// A line a person types into a planning lane's pane starts a turn as the worker's own input does, and the lane
// works until that turn ends: its wait for its person does not run out under the turn, however long it takes.
func TestAPlanningLaneWorkingOnATypedLineIsNotParked(t *testing.T) {
	t.Parallel()
	r := planningRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n", func(*fake.AgentDriver) {})
	working := newGate()
	r.gates(working)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, completed(0.01)},
		[]fake.DriverStep{working.step(), r.reports(workorders.OutcomeCompleted), completed(0.02)},
	))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.waitsForItsPerson(t)
	if err := r.driver.Type("lane-1", 1, "Put the sailings under each berth."); err != nil {
		t.Fatal(err)
	}
	r.reach(t, working)
	at := time.Now().UnixMilli()
	r.promptAt(t, at)
	r.callAt(t, "PreToolUse", "tool-1", at+1)
	for range 11 {
		r.clock.Advance(time.Minute)
		settle()
	}
	if parking := r.parkingOf(t); parking != nil {
		t.Fatalf("a planning lane at work on a line its person typed parked: %+v", parking)
	}
	if view := r.read(); view.ActualState != string(lanes.Working) {
		t.Fatalf("a planning lane at work on a line its person typed reads %s", view.ActualState)
	}
	r.callAt(t, "PostToolUse", "tool-1", at+2)
	working.free()
	r.awaitOrderState(t, workorders.Acceptance)
}

// The report of background work starts a turn of its own, and a lane in that turn works: a draining fleet parks it
// only past the drain deadline, as any working lane.
func TestADrainLeavesATurnBackgroundWorkStartedToRun(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, profileAt+"[lifecycle]\ndrain_deadline = \"5m\"\nhandoff_timeout = \"30ms\"\n")
	stopped, working := newGate(), newGate()
	r.gates(stopped, working)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, stopped.step(), completed(0.01), working.step(), r.reports(workorders.OutcomeCompleted), completed(0.02),
	}))
	r.atGate(stopped)
	at := time.Now().UnixMilli()
	r.stopAt(t, at, 1)
	stopped.free()
	r.reach(t, working)
	r.restsOnBackground()
	r.promptAt(t, at+1)
	r.drainFleet(t)
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if parking := r.parkingOf(t); parking != nil {
		t.Fatalf("a draining fleet parked a lane inside a turn its background work started: %+v", parking)
	}
	if view := r.read(); view.ActualState != string(lanes.Working) {
		t.Fatalf("a lane in a turn its background work started reads %s", view.ActualState)
	}
	working.free()
	r.awaitOrderState(t, workorders.Acceptance)
}

// A threshold crossed in a turn that background work started is crossed by a working lane: the instruction to hand
// off goes on the mid-turn path, and nothing is typed into the session as if it were at rest.
func TestAThresholdInATurnBackgroundWorkStartedHandsOffMidTurn(t *testing.T) {
	t.Parallel()
	r := contextLane(t)
	stopped, resumed, g := newGate(), newGate(), newGate()
	r.gates(stopped, resumed, g)
	answer := fake.DriverStep{Do: func(_ ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.startOn(
		[]fake.DriverStep{{Bound: true}, tokens(30), stopped.step(), completed(0.01), resumed.step(), tokens(185), g.step(), completed(0.02)},
		[]fake.DriverStep{answer, completed(0.03)},
	)
	r.reach(t, stopped)
	at := time.Now().UnixMilli()
	r.stopAt(t, at, 1)
	stopped.free()
	r.reach(t, resumed)
	r.restsOnBackground()
	r.promptAt(t, at+1)
	resumed.free()
	var instruction ports.Input
	r.eventually("the lane acted on its threshold", func(ledger.LaneView) bool {
		for _, input := range r.driver.Sent() {
			if strings.Contains(input.Text, lanes.ToolLaneHandoff) {
				instruction = input
				return true
			}
		}
		return r.driver.Compacts() != 0
	})
	if got := r.driver.Compacts(); got != 0 {
		t.Fatalf("a lane in a turn its background work started was told to compact as if at rest (%d)", got)
	}
	if !instruction.MidTurn {
		t.Fatalf("the instruction to a lane in a turn its background work started took the path of one at rest: %+v", instruction)
	}
	r.admit(instruction)
	g.free()
	r.awaitSecondGeneration(t)
}
