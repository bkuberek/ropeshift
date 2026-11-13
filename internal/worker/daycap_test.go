package worker_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// oneGenerationADay leaves a lane its first generation and no next one, with a crash window a crash
// would fill, so what stops a planned generation here is the day alone.
const oneGenerationADay = profileAt + `[lifecycle]
max_restarts = 1
restart_window = "10m"
max_generations_per_day = 1
restart_backoff = "30s"
handoff_timeout = "3m"
`

// spareDay has room for every generation here, with the same window.
const spareDay = profileAt + `[lifecycle]
max_restarts = 1
restart_window = "10m"
max_generations_per_day = 8
restart_backoff = "30s"
handoff_timeout = "3m"
`

// answersHandoff is a session that writes its narrative.
func answersHandoff(r **handoffRig) []fake.DriverStep {
	return []fake.DriverStep{{Do: func(ports.SessionSpec) { (*r).callHandoff(narrativeText) }}, completed(0.02)}
}

func (r *laneRig) reset(t *testing.T) {
	t.Helper()
	if err := r.plane2(t).ResetLane(t.Context(), scope, r.id, member); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// A handoff is a generation, and the day limits generations: a lane that keeps filling its context
// stops where a person can look, with its handoff kept, and a reset goes on from that handoff.
func TestAHandoffPastTheDayWaitsForAMember(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWorkOn(t, oneGenerationADay, (*laneRig).toldToGoOn, answersHandoff(&r)...)
	r.handOff(t)
	r.laneState(lanes.Tripped)
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Fatalf("a generation past the day: %d sessions", len(specs))
	}
	r.reset(t)
	r.awaitSecondGeneration(t)
	specs := r.driver.Specs()
	if len(specs) != 2 {
		t.Fatalf("%d sessions after the reset", len(specs))
	}
	generations := r.generations(t)
	if len(generations) != 2 || generations[1].StartReason != ledger.StartHandoff || generations[1].HandoffIn == "" {
		t.Errorf("a reset lost the handoff: %+v", generations)
	}
	if change := r.outcome(); change.Outcome != workorders.OutcomeCompleted {
		t.Errorf("outcome %+v", change)
	}
}

// A handoff ignores the breaker's window, and a window full of crashes stops none.
func TestAHandoffIgnoresACrashWindowItCouldFill(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWorkOn(t, spareDay, (*laneRig).toldToGoOn, answersHandoff(&r)...)
	now := r.clock.Now().UnixMilli()
	if _, err := r.journal.ChargeRestart(t.Context(), "lane-1", lanes.Restart{Key: "a-crash", AtMs: now, NotBeforeMs: now}); err != nil {
		t.Fatal(err)
	}
	r.handOff(t)
	r.awaitSecondGeneration(t)
	if specs := r.driver.Specs(); len(specs) != 2 {
		t.Errorf("a full window stopped a handoff: %d sessions", len(specs))
	}
}

// Every autonomy bound reads the same day, so a worker held below unattended holds a handoff too.
func TestAHandoffPastAWorkersBoundWaitsForAMember(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWorkOn(t, spareDay, (*laneRig).toldToGoOn, answersHandoff(&r)...)
	worker := r.subject.Auth().WorkerID
	ops := control.NewAutonomy(r.store)
	if err := ops.SetLevel(t.Context(), scope, worker, authority.LevelAttended, member); err != nil {
		t.Fatal(err)
	}
	bound := authority.DefaultAutonomyPolicy()
	bound.Version, bound.AttendedMaxRespawnsPerDay = 2, 1
	if _, err := ops.PublishAutonomyPolicy(t.Context(), scope, authority.AutonomyDraft{Body: "[autonomy_policy]\nversion = 2\n", Policy: bound}, member); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.handOff(t)
	r.laneState(lanes.Tripped)
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("a generation past the bound: %d sessions", len(specs))
	}
}

// A request for a new generation is one, whoever asked.
func TestARespawnPastTheDayWaitsForAMember(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, oneGenerationADay)
	g, after := newGate(), newGate()
	r.finishLater(g)
	r.gates(g, after)
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
	r.laneState(lanes.Tripped)
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Fatalf("a generation past the day: %d sessions", len(specs))
	}
	r.reset(t)
	r.reach(t, after)
	close(after.release)
	r.awaitOrderState(t, workorders.Acceptance)
	generations := r.generations(t)
	if len(generations) != 2 || generations[1].StartReason != ledger.StartUserRespawn {
		t.Errorf("generations %+v", generations)
	}
}

func parkedAndWokenOn(t *testing.T, profile string) *handoffRig {
	t.Helper()
	var r *handoffRig
	r = atWorkOn(t, profile, (*laneRig).toldToGoOn, answersHandoff(&r)...)
	r.park(t, humanPark())
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	r.wake(t)
	return r
}

// A wake brings a lane back with nobody there, outside a window, and it comes up to the bound on
// generations and no further, because that is what stands in for a person who is not there.
func TestAResumePastTheDayWaitsForAMember(t *testing.T) {
	t.Parallel()
	r := parkedAndWokenOn(t, oneGenerationADay)
	r.pass(t)
	r.laneState(lanes.Tripped)
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Fatalf("a generation past the day: %d sessions", len(specs))
	}
	r.reset(t)
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
	if specs := r.driver.Specs(); len(specs) != 2 {
		t.Errorf("%d sessions after the reset", len(specs))
	}
	generations := r.generations(t)
	if len(generations) != 2 || generations[1].StartReason == ledger.StartReset {
		t.Errorf("generations %+v", generations)
	}
}

// A worker that comes back reads a stopped handoff from its journal, stays stopped, and hands the
// next generation what the first one wrote.
func TestAHandoffPastTheDayOutlivesItsWorker(t *testing.T) {
	var r *handoffRig
	r = atWorkOn(t, oneGenerationADay, (*laneRig).toldToGoOn, answersHandoff(&r)...)
	r.handOff(t)
	r.laneState(lanes.Tripped)
	next, driver := r.workerDies(t, lanes.Session{Found: true, Liveness: lanes.LivenessDead}, r.toldToGoOn())
	r.subject = next
	if _, err := next.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.eventually("the next worker holds it", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Tripped) })
	time.Sleep(20 * time.Millisecond)
	if specs := driver.Specs(); len(specs) != 0 {
		t.Fatalf("a restart spent the day: %d sessions", len(specs))
	}
	r.reset(t)
	next.Wait()
	specs := driver.Specs()
	if len(specs) != 1 || specs[0].Generation != 2 {
		t.Fatalf("specs %+v", specs)
	}
	if _, doc := r.storedHandoff(t, 1); doc.Narrative == nil {
		t.Errorf("the handoff lost its narrative: %+v", doc)
	}
}
