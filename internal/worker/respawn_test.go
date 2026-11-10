package worker_test

import (
	"errors"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A person's reset ends a crash loop, so the lane starts from its pinned brief and not from the
// session that kept crashing.
func TestAResetStartsFromThePinnedBrief(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, restarts)
	r.driver.Play(script(crashed()))
	r.start()
	r.laneState(lanes.Backoff)
	r.clock.Advance(31 * time.Second)
	r.awaitGeneration(t, 2)
	r.laneState(lanes.Backoff)
	r.clock.Advance(61 * time.Second)
	r.awaitGeneration(t, 3)
	r.laneState(lanes.Tripped)
	resumed := len(r.driver.Resumed())
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	if err := r.plane2(t).ResetLane(t.Context(), scope, r.id, member); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if got := r.driver.Resumed(); len(got) != resumed {
		t.Errorf("a reset resumed the session that crashed: %+v", got)
	}
	generations := r.generations(t)
	if last := generations[len(generations)-1]; last.StartReason != ledger.StartReset || last.HandoffIn != "" {
		t.Errorf("the generation after a reset: %+v", last)
	}
}

// A member asks for a new generation of a lane that is running: the session stops, and the next
// one goes on from it, once, however many passes read the request.
func TestAMemberRespawnsARunningLane(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
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
	for range 3 {
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	g.release <- struct{}{}
	r.reach(t, after)
	for range 3 {
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	// A session reaches its gate ahead of the worker that reads it, so the lane gets a moment to read
	// `working`. A second respawn would start a third session, which the count below reads.
	r.eventually("the next session at work", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	close(after.release)
	r.awaitOrderState(t, workorders.Acceptance)
	generations := r.generations(t)
	if len(generations) != 2 || generations[1].StartReason != ledger.StartUserRespawn || generations[0].EndReason != string(ledger.StartUserRespawn) {
		t.Fatalf("generations %+v", generations)
	}
	if specs := r.driver.Specs(); len(specs) != 2 {
		t.Errorf("%d sessions", len(specs))
	}
}

// Only a principal that may ask for one can, and a lane is not one.
func TestARespawnNeedsTheRightToAsk(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	err := r.plane2(t).RespawnLane(t.Context(), scope, r.id, ledger.Actor{Kind: ledger.ActorLane, ID: "lane-1"})
	if err == nil {
		t.Fatal("a lane asked for a new generation")
	}
	if !errors.Is(err, workorders.ErrForbidden) {
		t.Errorf("err %v", err)
	}
}
