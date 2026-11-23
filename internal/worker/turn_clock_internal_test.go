package worker

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A wall clock that steps back leaves every hook the lane kept stamped after the clock's new time, and the turn clock
// compares hook times: a prompt and a Stop of the next turn would each be no newer than what was kept, and the lane
// would read no turn begin or end until the clock caught up. A hook cannot have fired after now, so the worker takes
// each time kept past now as now.
func TestAClockThatStepsBackStillEndsTheTurn(t *testing.T) {
	t.Parallel()
	journal, err := fake.NewJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	clock := fake.NewClock(time.UnixMilli(1_000_000))
	w := New(nil, journal, clock, Config{Report: func(error) {}})
	run := &laneRun{w: w, rt: &LaneRuntime{}, lease: ledger.DesiredLease{Lane: "lane-1"}, state: newLaneState(lanes.Idle, clock.Now()), generation: 1}
	// No session reads this lane's stream, so nothing waits on a turn's end beside the test.
	run.sessionOver.Store(true)
	prompt := func(atMs int64) lanes.HookEvent {
		return lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "session-1", SessionID: "session-1", AtMs: atMs}
	}
	stop := func(atMs int64) lanes.HookEvent {
		return lanes.HookEvent{Event: lanes.EventStop, Subject: "session-1", SessionID: "session-1", AtMs: atMs}
	}

	at := clock.Now().UnixMilli()
	run.observeHook(t.Context(), prompt(at+1))
	run.observeHook(t.Context(), stop(at+2))
	if run.state.turnUnended() {
		t.Fatal("a turn whose Stop was heard reads as running")
	}
	run.signal(t.Context(), lanes.OnTurnFinished)

	clock.Advance(-time.Hour)
	back := clock.Now().UnixMilli()
	run.observeHook(t.Context(), prompt(back+1))
	if state := run.state.current(); state != lanes.Working || !run.state.turnUnended() {
		t.Fatalf("a prompt after the clock stepped back reads %s, running %v, want a turn begun", state, run.state.turnUnended())
	}
	run.observeHook(t.Context(), stop(back+2))
	if run.state.turnUnended() {
		t.Error("a Stop after the clock stepped back ends no turn")
	}
	run.signal(t.Context(), lanes.OnTurnFinished)
	if state := run.state.current(); state != lanes.Idle {
		t.Errorf("the lane reads %s, want idle", state)
	}
}

// A time the clock has not reached stays as a hook's: only a time more than a second past now cannot be one.
func TestAClampToTheClockLeavesWhatTheClockHasReached(t *testing.T) {
	t.Parallel()
	state := &laneState{stoppedMs: 5_000, failedMs: 6_500, promptedMs: 9_000, handedMs: 5_001}
	state.clampToClock(5_000)
	if state.stoppedMs != 5_000 || state.failedMs != 5_000 || state.promptedMs != 5_000 || state.handedMs != 5_001 {
		t.Errorf("the clamp left stopped %d, failed %d, prompted %d, handed %d", state.stoppedMs, state.failedMs, state.promptedMs, state.handedMs)
	}
}
