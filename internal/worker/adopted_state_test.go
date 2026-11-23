package worker_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// aLaneThatSavedAfterATurn leaves a lane at rest after a first turn that ended while the lane was at work, with the
// session still held, and returns once the journal holds what that turn's end saved. The lane's turn runs while the test
// holds it at a gate, so the test acts on the lane in the middle of the turn through before.
func aLaneThatSavedAfterATurn(t *testing.T, before func(*laneRig)) *laneRig {
	t.Helper()
	g := newGate()
	r := laneRigWith(t, "")
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), completed(0.01)}, []fake.DriverStep{{Hold: true}}))
	r.start()
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached its gate")
	}
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	before(r)
	g.release <- struct{}{}
	r.eventually("the turn's end saved", func(ledger.LaneView) bool {
		record, err := r.journal.Lane(t.Context(), "lane-1")
		return err == nil && record.Progress.Cost.CostUSD != 0
	})
	return r
}

// A lane past a wait it declared reads stalled again, and a lane a restarted worker takes over is no different: the wait
// it declared is in the journal, so the worker does not read it as stalled at once.
func TestAnAdoptedRunKeepsItsDeclaredWait(t *testing.T) {
	t.Parallel()
	var until time.Time
	r := aLaneThatSavedAfterATurn(t, func(r *laneRig) {
		if reply := r.wait(t, 2); !reply.OK {
			t.Fatalf("the wait was refused: %+v", reply)
		}
		until = r.clock.Now().Add(2 * time.Minute)
	})
	driver := r.takeOver(t, script([]fake.DriverStep{{Hold: true}}))
	r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
	if got := r.subject.LaneWaitUntil("lane-1"); !got.Equal(until) {
		t.Errorf("the adopted run's declared wait ends at %v, want %v", got, until)
	}
}

// A wait a lane declares is kept when it is declared, and not at the next turn's end: a restart in between would lose it, and
// the worker that took the lane over would read it as stalled at once.
func TestADeclaredWaitIsKeptBeforeTheNextTurnEnds(t *testing.T) {
	t.Parallel()
	r := aLaneThatSavedAfterATurn(t, func(*laneRig) {})
	if reply := r.wait(t, 2); !reply.OK {
		t.Fatalf("the wait was refused: %+v", reply)
	}
	until := r.clock.Now().Add(2 * time.Minute)
	r.eventually("the wait kept", func(ledger.LaneView) bool {
		record, err := r.journal.Lane(t.Context(), "lane-1")
		return err == nil && record.Progress.WaitUntilMs == until.UnixMilli()
	})
	driver := r.takeOver(t, script([]fake.DriverStep{{Hold: true}}))
	r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
	if got := r.subject.LaneWaitUntil("lane-1"); !got.Equal(until) {
		t.Errorf("the adopted run's declared wait ends at %v, want %v", got, until)
	}
}

// A model a lane's session resolved is known to the run that takes the lane over, so a clear in that run binds its
// generation with the model the first one ran on.
func TestAnAdoptedRunKeepsItsModel(t *testing.T) {
	t.Parallel()
	r := aLaneThatSavedAfterATurn(t, func(*laneRig) {})
	driver := r.takeOver(t, script([]fake.DriverStep{{Hold: true}}))
	r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
	if reply := r.cleared(t, clearedSession); !reply.OK || reply.Generation != 2 {
		t.Fatalf("reply %+v", reply)
	}
	r.read()
	var binding workorders.GenerationBinding
	for _, event := range r.events() {
		var change workorders.Change
		if event.Type != workorders.EventGenerationBound || json.Unmarshal(event.Data, &change) != nil || change.Generation != 2 {
			continue
		}
		if err := json.Unmarshal(change.Payload, &binding); err != nil {
			t.Fatal(err)
		}
	}
	if binding.ModelRequested == "" || binding.ModelResolved != "resolved-model" {
		t.Errorf("a clear after the adoption bound its generation with the model %q, resolved %q, want the first generation's", binding.ModelRequested, binding.ModelResolved)
	}
	if settings := binding.Settings; settings == nil || settings.ModelRequested != binding.ModelRequested || settings.ModelResolved != "resolved-model" {
		t.Errorf("the binding's settings are %+v", settings)
	}
}

// The day's verdict outlives a restart: the run a restarted worker rebuilds from the journal of an orchestrator whose day
// tripped its breaker reads that breaker as tripped when the day rolls on, and does not cool as one its window tripped
// does (ADR-0014).
func TestARestartedOrchestratorKeepsItsDayVerdict(t *testing.T) {
	crash := script([]fake.DriverStep{{Bound: true}, {Crash: true}})
	o := newOrchestratorRig(t, crash, crash, crash, aSession())
	o.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser(dayCapProfile)
	row := o.start(t)
	o.tick(t)
	o.asked(t, "q1", "Which retry budget?")
	o.awaitNoted(t, 1)
	o.clock.Advance(31 * time.Second)
	o.awaitSessions(t, 1)
	o.await(t, "the first backoff", func(row ledger.Orchestrator) bool { return row.Actual == string(lanes.Backoff) })
	o.clock.Advance(31 * time.Second)
	o.awaitSessions(t, 2)
	o.await(t, "the second backoff", func(row ledger.Orchestrator) bool { return row.Actual == string(lanes.Backoff) })
	o.clock.Advance(61 * time.Second)
	o.awaitSessions(t, 3)
	o.await(t, "a trip on the day", func(row ledger.Orchestrator) bool {
		return row.Tripped() && slices.Contains(row.Conditions, string(lanes.DayCapped))
	})

	o.clock.Advance(25 * time.Hour)
	cools, err := o.subject.RebuiltRunCools(t.Context(), row.Lane())
	if err != nil {
		t.Fatal(err)
	}
	if cools {
		t.Error("the run a restarted worker rebuilds reads a breaker its day tripped as cooled, a day on")
	}
}

// The day's verdict holds on the path a restart really takes: the worker that starts after a tripped orchestrator's
// worker rebuilds no run for it, so no verdict is judged again, and the orchestrator reads tripped on its day, with no
// session started for it, a day on, until a member resets it (ADR-0014).
func TestATrippedOrchestratorStaysDayCappedAcrossARestartAndADay(t *testing.T) {
	crash := script([]fake.DriverStep{{Bound: true}, {Crash: true}})
	o := newOrchestratorRig(t, crash, crash, crash, aSession())
	o.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser(dayCapProfile)
	row := o.start(t)
	o.tick(t)
	o.asked(t, "q1", "Which retry budget?")
	o.awaitNoted(t, 1)
	o.clock.Advance(31 * time.Second)
	o.awaitSessions(t, 1)
	o.await(t, "the first backoff", func(row ledger.Orchestrator) bool { return row.Actual == string(lanes.Backoff) })
	o.clock.Advance(31 * time.Second)
	o.awaitSessions(t, 2)
	o.await(t, "the second backoff", func(row ledger.Orchestrator) bool { return row.Actual == string(lanes.Backoff) })
	o.clock.Advance(61 * time.Second)
	o.awaitSessions(t, 3)
	o.await(t, "a trip on the day", func(row ledger.Orchestrator) bool {
		return row.Tripped() && slices.Contains(row.Conditions, string(lanes.DayCapped))
	})

	o.restarted(t)
	o.clock.Advance(25 * time.Hour)
	for range 6 {
		if _, err := o.subject.Reconcile(o.ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := o.row(t); !got.Tripped() || !slices.Contains(got.Conditions, string(lanes.DayCapped)) {
		t.Errorf("a day after the restart the orchestrator reads %q with conditions %v, want tripped on its day", got.Actual, got.Conditions)
	}
	if n := len(o.specs()); n != 3 {
		t.Errorf("%d sessions started for an orchestrator whose day tripped its breaker, want the 3 that tripped it", n)
	}
	if o.subject.Running(row.Lane()) {
		t.Error("the restarted worker runs a tripped orchestrator that only a member's reset starts again")
	}
}
