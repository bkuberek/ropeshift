package worker

import (
	"context"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// A generation with a new process has no wait on a turn, on a refused nudge, or on an end of the session before it:
// that session is gone, and a bound of its wait would decide the new session's rest. A clear keeps the process and the
// state the lane reads, so a turn it waited on stays bounded.
func TestANewGenerationKeepsNoTurnStateOfTheOneBefore(t *testing.T) {
	t.Parallel()
	clock := fake.NewClock(time.UnixMilli(1_000_000))
	run := &laneRun{w: &Worker{clock: clock}, state: newLaneState(lanes.Working, clock.Now()), holding: &ports.TurnResult{Status: ports.TurnCompleted}, holdingIn: 1, endHeld: true, endMs: 5}
	run.state.noteTurnWait(clock.Now())
	run.state.noteRefusal(clock.Now())
	run.enterGeneration(2, "", lanes.Progress{})
	if since, waiting := run.state.turnWait(); waiting {
		t.Errorf("a new generation waits on the turn of the one before, since %s", since)
	}
	if since, refused := run.state.refusal(); refused {
		t.Errorf("a new generation waits on the refused nudge of the one before, since %s", since)
	}
	if run.holding != nil || run.endHeld || run.endMs != 0 {
		t.Errorf("a new generation holds the end of the one before: %+v, held %v at %d", run.holding, run.endHeld, run.endMs)
	}

	cleared := newLaneState(lanes.Working, clock.Now())
	cleared.noteTurnWait(clock.Now())
	cleared.newGeneration(clock.Now(), 0)
	if _, waiting := cleared.turnWait(); !waiting {
		t.Error("a clear ended the wait on the turn its process is in")
	}
}

// letGoLink answers an orchestrator's let_go, the one call a rest makes of the control plane.
type letGoLink struct{ ports.WorkerLink }

func (letGoLink) Orchestrate(context.Context, string, ledger.OrchestratorCall) (ledger.OrchestratorReply, error) {
	return ledger.OrchestratorReply{}, nil
}

// A rest the live wait's bound decided has no end the session reported, so nothing says the session took the wake it
// was started on: the wake goes again with the next one, and is not kept as landed. An end the session reported and
// the worker held, because input came after it, says it did.
func TestAnOrchestratorsQuietBoundLandsOnlyAWakeItsSessionTook(t *testing.T) {
	t.Parallel()
	for name, heard := range map[string]bool{"no end heard": false, "an end heard and held": true} {
		t.Run(name, func(t *testing.T) {
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
			w := New(letGoLink{}, journal, clock, Config{Report: func(error) {}})
			item := ledger.WakeItem{Kind: ledger.WakeQuestion, ID: "q1", Stamp: 1, Text: "Which retry budget?"}
			w.wakes.Note([]ledger.WakeItem{item}, clock.Now())
			run := &laneRun{w: w, rt: &LaneRuntime{}, lease: ledger.DesiredLease{Lane: "orchestrator-1"}, state: newLaneState(lanes.Working, clock.Now()), generation: 1}
			run.setWake([]ledger.WakeItem{item})
			if heard {
				run.holding, run.holdingIn = &ports.TurnResult{Status: ports.TurnCompleted}, 1
			}
			run.state.noteTurnWait(clock.Now())
			clock.Advance(lanes.MaxLiveWait(nil) + time.Second)
			run.turnBound(t.Context())
			if run.state.current() != lanes.Idle || len(run.pendingWake()) != 0 {
				t.Fatalf("the bound left the run %s with %+v pending", run.state.current(), run.pendingWake())
			}
			if _, due := w.wakeDue(); due == heard {
				t.Errorf("with %s, the wake is due again: %v", name, due)
			}
		})
	}
}
