package worker_test

import (
	"errors"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// hungProfile puts both holds inside the limit on a compaction.
const hungProfile = profileAt + "[lifecycle]\nask_wait = \"5m\"\nstall_after = \"20m\"\ndrain_deadline = \"5m\"\nhandoff_timeout = \"30ms\"\n"

func (r *laneRig) compacting(t *testing.T) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: "PreCompact", Subject: "s", Detail: "auto", AtMs: 1})
	r.eventually("a compaction", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Compacting) })
}

// aLaneAsking rests on a blocking question, in a compaction it started at rest.
func aLaneAsking(t *testing.T) *laneRig {
	t.Helper()
	r := laneRigWith(t, hungProfile)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.asking("What is the retry budget?"), completed(0.01)}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	r.compacting(t)
	return r
}

// No process waits on a person, and a compaction that stopped moving is the one thing that can hold
// a process past `ask_wait`. `stall_after` is what says it stopped, and a compaction inside it goes on.
func TestAWaitOnAnAnswerEndsWhenACompactionStalls(t *testing.T) {
	r := aLaneAsking(t)
	r.clock.Advance(19 * time.Minute)
	time.Sleep(5 * superviseStep)
	if view := r.read(); view.ActualState != string(lanes.Compacting) {
		t.Fatalf("a compaction inside its limit reads %s", view.ActualState)
	}
	r.clock.Advance(2 * time.Minute)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("a session in a hung compaction was told %+v", sent)
	}
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Actual != lanes.Parked {
		t.Errorf("the lane reads %s", record.Actual)
	}
}

// A profile with no limit keeps the wait on a compaction, because nothing says it stopped.
func TestWithNoLimitAWaitSitsOutACompaction(t *testing.T) {
	r := laneRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nstall_after = \"0s\"\n")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.asking("What is the retry budget?"), completed(0.01)}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	r.compacting(t)
	r.clock.Advance(24 * time.Hour)
	time.Sleep(5 * superviseStep)
	if view := r.read(); view.ActualState != string(lanes.Compacting) {
		t.Fatalf("the lane reads %s", view.ActualState)
	}
	r.driver.StopEverySession()
	r.subject.Wait()
}

// A compaction that stalled with no question open holds nothing, so the design leaves it to whoever
// reads `stalled`: the lane keeps its session.
func TestAStalledCompactionWithNothingToAnswerStays(t *testing.T) {
	r := laneRigWith(t, hungProfile)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.compacting(t)
	r.clock.Advance(21 * time.Minute)
	time.Sleep(5 * superviseStep)
	if view := r.read(); view.ActualState != string(lanes.Compacting) {
		t.Fatalf("the lane reads %s", view.ActualState)
	}
}

// A draining fleet's deadline is the same bound for a compaction as for a turn: a lane parks by it
// where it stands.
func TestADrainParksACompactionPastItsDeadline(t *testing.T) {
	r := atWorkOn(t, hungProfile, (*laneRig).toldToGoOn, fake.DriverStep{Hold: true})
	r.compacting(t)
	r.drainFleet(t)
	parking := func() *lanes.ParkProgress {
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
		record, err := r.journal.Lane(t.Context(), "lane-1")
		if err != nil {
			t.Fatal(err)
		}
		return record.Parking
	}
	if parking() != nil {
		t.Fatal("a compaction inside the deadline was stopped")
	}
	r.clock.Advance(5 * time.Minute)
	if parking() == nil {
		t.Fatal("a compaction past the deadline went on")
	}
	close(r.release)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
}

// Only the two holds above may take a lane out of a compaction. A park that asks for any other reason
// waits for it, as it always has.
func TestACompactionRefusesAnyOtherPark(t *testing.T) {
	r := laneRigWith(t, hungProfile)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.compacting(t)
	err := r.subject.Park(t.Context(), "lane-1", lanes.ParkRequest{Trigger: lanes.ParkFleetDrain, Expr: lanes.HumanWake()})
	if !errors.Is(err, worker.ErrNoParking) {
		t.Errorf("err %v", err)
	}
	if view := r.read(); view.ActualState != string(lanes.Compacting) {
		t.Errorf("the lane reads %s", view.ActualState)
	}
}
