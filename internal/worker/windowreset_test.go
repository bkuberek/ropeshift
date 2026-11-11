package worker_test

import (
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// resetWindow is an orchestrator's reset as the ledger keeps it: one more reset of the breaker's window,
// which the worker reads the way it reads a member's.
func (r *laneRig) resetWindow(t *testing.T) {
	t.Helper()
	err := r.store.Write(t.Context(), scope, "lane.reset", func(tx ports.WriteTx) error {
		if _, err := tx.Lanes().ResetWindow("lane-1"); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventLaneReset, Actor: ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "w"}, Subject: ledger.Subject{Kind: ledger.SubjectLane, ID: "lane-1"},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// tripOnTheWindow crashes a lane until its breaker's window trips it.
func (r *laneRig) tripOnTheWindow(t *testing.T) {
	t.Helper()
	r.driver.Play(script(crashed()))
	r.start()
	r.laneState(lanes.Backoff)
	r.clock.Advance(31 * time.Second)
	r.awaitGeneration(t, 2)
	r.laneState(lanes.Backoff)
	r.clock.Advance(61 * time.Second)
	r.awaitGeneration(t, 3)
	r.laneState(lanes.Tripped)
}

// An orchestrator's reset closes a breaker the window tripped, and the crashes it cleared still count
// toward the lane's day, so no reset of an orchestrator's overrides the daily cap (ADR-0014).
func TestAWindowResetRestartsALaneAndKeepsItsDay(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, restarts)
	r.tripOnTheWindow(t)
	if view := r.read(); slices.Contains(view.Conditions, string(lanes.DayCapped)) {
		t.Fatalf("a lane its window tripped reads %v", view.Conditions)
	}
	before, err := r.journal.Restarts(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	r.resetWindow(t)
	r.subject.Wait()
	if specs := r.driver.Specs(); len(specs) != 4 {
		t.Fatalf("sessions after the reset: %d", len(specs))
	}
	if change := r.outcome(); change.Outcome != workorders.OutcomeCompleted {
		t.Errorf("outcome %+v", change)
	}
	after, err := r.journal.Restarts(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) <= len(before) || slices.ContainsFunc(after[:len(before)], func(c lanes.Restart) bool { return !c.Planned }) {
		t.Errorf("the reset dropped what the day counts, or kept the window: before %+v, after %+v", before, after)
	}
}

// A lane its day tripped says so, and an orchestrator's reset starts nothing there: only a member's
// reset overrides the day's verdict.
func TestOnlyAMembersResetOverridesTheDay(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, oneGenerationADay)
	g, after := newGate(), newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, after.step(), r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	r.start()
	r.reach(t, g)
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	if err := r.plane2(t).RespawnLane(t.Context(), scope, r.id, member); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	g.release <- struct{}{}
	r.eventually("tripped on its day", func(view ledger.LaneView) bool {
		return view.ActualState == string(lanes.Tripped) && slices.Contains(view.Conditions, string(lanes.DayCapped))
	})
	r.resetWindow(t)
	time.Sleep(100 * time.Millisecond)
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Fatalf("a window reset started a generation past the day: %d sessions", len(specs))
	}
	if view := r.read(); view.ActualState != string(lanes.Tripped) {
		t.Fatalf("after a window reset the lane reads %+v", view)
	}
	r.reset(t)
	r.reach(t, after)
	if view := r.read(); slices.Contains(view.Conditions, string(lanes.DayCapped)) {
		t.Errorf("a lane a member reset still reads %v", view.Conditions)
	}
	close(after.release)
	r.awaitOrderState(t, workorders.Acceptance)
}
