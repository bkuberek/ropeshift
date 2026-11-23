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
	run.observeHook(t.Context(), prompt(at))
	run.observeHook(t.Context(), stop(at+1))
	if run.state.turnUnended() {
		t.Fatal("a turn whose Stop was heard reads as running")
	}
	run.signal(t.Context(), lanes.OnTurnFinished)

	clock.Advance(-time.Hour)
	back := clock.Now().UnixMilli()
	// A hook stamped at the clock's new time is the first the lane hears after the step, so it must read as newer than what was kept.
	run.observeHook(t.Context(), prompt(back))
	if state := run.state.current(); state != lanes.Working || !run.state.turnUnended() {
		t.Fatalf("a prompt after the clock stepped back reads %s, running %v, want a turn begun", state, run.state.turnUnended())
	}
	run.observeHook(t.Context(), stop(back+1))
	if run.state.turnUnended() {
		t.Error("a Stop after the clock stepped back ends no turn")
	}
	run.signal(t.Context(), lanes.OnTurnFinished)
	if state := run.state.current(); state != lanes.Idle {
		t.Errorf("the lane reads %s, want idle", state)
	}
}

// A time the clock has not reached stays as a hook's: only a time more than a second past now cannot be one. One that
// cannot be stands a millisecond before the hook being read, or before now when that hook is stamped later or not at all.
func TestAClampToTheClockLeavesWhatTheClockHasReached(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		at   int64
		want int64
	}{
		{"a hook stamped before now", 4_900, 4_899},
		{"a hook stamped at now", 5_000, 4_999},
		{"a hook stamped after now", 5_400, 4_999},
		{"a hook with no stamp", 0, 4_999},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := &laneState{stoppedMs: 5_000, failedMs: 6_500, promptedMs: 9_000, handedMs: 5_001}
			state.clampToClock(5_000, tt.at)
			if state.stoppedMs != 5_000 || state.failedMs != tt.want || state.promptedMs != tt.want || state.handedMs != 5_001 {
				t.Errorf("the clamp left stopped %d, failed %d, prompted %d, handed %d, want a clamped time of %d", state.stoppedMs, state.failedMs, state.promptedMs, state.handedMs, tt.want)
			}
		})
	}
}

// A turn cannot end in the millisecond it began, so a Stop in the newest prompt's millisecond is the end of the turn before
// it, and that turn's background work is over: the Stop keeps no count of it, as one older than the prompt keeps none.
func TestAStopInThePromptsMillisecondKeepsNoBackgroundCount(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		stopAt int64
		tasks  int
	}{
		{"before the prompt", 4_999, 0},
		{"in its millisecond", 5_000, 0},
		{"after it", 5_001, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			now := time.UnixMilli(1_000_000)
			state := newLaneState(lanes.Idle, now)
			state.notePrompt(5_000)
			state.noteStop(2, now, tt.stopAt)
			if _, tasks, resting := state.backgroundRest(); tasks != tt.tasks || resting != (tt.tasks > 0) {
				t.Errorf("a Stop at %d after a prompt at 5000 keeps %d background tasks, resting %v, want %d", tt.stopAt, tasks, resting, tt.tasks)
			}
		})
	}
}
