package worker_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
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

// lookupGate holds one lookup of a lane's credential. A worker makes it for each generation after the journal
// has the generation and before the session starts, so a test holds a lane between the two.
type lookupGate struct {
	fake.SecretResolver
	mu      sync.Mutex
	lookups int
	holds   int
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func (l *lookupGate) Resolve(ctx context.Context, reference string) (ports.SecretBinding, error) {
	l.mu.Lock()
	l.lookups++
	held := l.lookups == l.holds
	l.mu.Unlock()
	if held {
		close(l.reached)
		select {
		case <-l.release:
		case <-ctx.Done():
		}
	}
	return l.SecretResolver.Resolve(ctx, reference)
}

func (l *lookupGate) free() { l.once.Do(func() { close(l.release) }) }

// await waits for the lane to be held, with its generation in the journal and no session started.
func (l *lookupGate) await(t *testing.T) {
	t.Helper()
	select {
	case <-l.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the lane never began the generation")
	}
}

// endsWithTheTest stops every session and waits for the lane when the test ends. It is registered before the
// gates and the lookup are, because those cleanups free what this waits for. A stop cannot interrupt a step a
// script runs, so a gate the test never released is let go by gates.
func (r *laneRig) endsWithTheTest() {
	r.t.Cleanup(func() {
		r.driver.StopEverySession()
		r.waitForTheLane(r.t)
	})
}

// holdsLookup holds the nth session start of the lane, and a test frees it.
func (r *laneRig) holdsLookup(n int) *lookupGate {
	r.t.Helper()
	l := &lookupGate{holds: n, reached: make(chan struct{}), release: make(chan struct{})}
	r.config.Secrets = l
	r.t.Cleanup(l.free)
	return l
}

// asksForANewGeneration is a member's request, which the worker reads on its next pass.
func (r *laneRig) asksForANewGeneration(t *testing.T) {
	t.Helper()
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
}

// noStopWithoutASession fails a test whose worker asked the driver to stop a session it does not hold.
func (r *laneRig) noStopWithoutASession(t *testing.T) {
	t.Helper()
	for _, err := range r.problems() {
		if errors.Is(err, ports.ErrNoSession) {
			t.Errorf("a stop was asked of a session the driver does not hold: %v", err)
		}
	}
}

// A pass that read the journal before a respawn's generation began sees the request as new, and reaches the
// run after that generation began. The run has acted on it: a stop then finds no session, spends the one stop
// the new generation has, and leaves a lane that cannot end.
func TestARespawnRequestThatArrivesAgainIsNotActedOnTwice(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	g, after := newGate(), newGate()
	r.endsWithTheTest()
	r.gates(g, after)
	held := r.holdsLookup(2)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, after.step(), r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	defer r.noStopWithoutASession(t)
	r.start()
	r.reach(t, g)
	r.asksForANewGeneration(t)
	g.release <- struct{}{}
	held.await(t)
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Generation != 2 || record.RespawnsSeen != 1 {
		t.Fatalf("the journal holds generation %d, having seen %d requests", record.Generation, record.RespawnsSeen)
	}
	r.subject.Respawn(t.Context(), "lane-1", 1)
	if view := r.read(); view.ActualState == string(lanes.Retiring) {
		t.Fatalf("a request the run had acted on retired the generation that followed it: %+v", view)
	}
	held.free()
	r.reach(t, after)
	r.eventually("the next session at work", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	close(after.release)
	r.awaitOrderState(t, workorders.Acceptance)
	generations := r.generations(t)
	if len(generations) != 2 || generations[1].StartReason != ledger.StartUserRespawn {
		t.Fatalf("generations %+v", generations)
	}
	if specs := r.driver.Specs(); len(specs) != 2 {
		t.Errorf("%d sessions", len(specs))
	}
}

// A request that comes while the next generation has no session yet is not lost: the session cannot take a
// stop before the driver holds it, so the lane stops it once it does. The second generation holds until that
// stop, so it ends through it however the session orders the stop against its script.
func TestARespawnAskedBeforeASessionStartsStopsItOnceItHas(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	g, after := newGate(), newGate()
	r.endsWithTheTest()
	r.gates(g, after)
	held := r.holdsLookup(2)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.driver.PlayAfter(
		script([]fake.DriverStep{{Bound: true}, {Hold: true}}),
		script([]fake.DriverStep{{Bound: true}, after.step(), r.reports(workorders.OutcomeCompleted), completed(0.01)}),
	)
	defer r.noStopWithoutASession(t)
	r.start()
	r.reach(t, g)
	r.asksForANewGeneration(t)
	g.release <- struct{}{}
	held.await(t)
	r.asksForANewGeneration(t)
	held.free()
	r.reach(t, after)
	r.eventually("the third session at work", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	// A third request would start a fourth session, which the count below reads.
	for range 3 {
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	close(after.release)
	r.awaitOrderState(t, workorders.Acceptance)
	generations := r.generations(t)
	if len(generations) != 3 || generations[1].StartReason != ledger.StartUserRespawn || generations[2].StartReason != ledger.StartUserRespawn {
		t.Fatalf("generations %+v", generations)
	}
	if generations[1].EndReason != string(ledger.StartUserRespawn) {
		t.Errorf("the second generation ended with %q, not through the respawn that was asked while it began", generations[1].EndReason)
	}
	if specs := r.driver.Specs(); len(specs) != 3 {
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
