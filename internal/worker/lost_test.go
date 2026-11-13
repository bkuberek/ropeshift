package worker_test

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// startGate holds a session's start before the driver holds the session, as a driver does while it
// launches one. A stop asked of the session then finds none.
type startGate struct {
	ports.AgentDriver
	reached chan struct{}
	release chan struct{}
	once    sync.Once
	freed   sync.Once
}

func newStartGate(driver ports.AgentDriver) *startGate {
	return &startGate{AgentDriver: driver, reached: make(chan struct{}), release: make(chan struct{})}
}

func (d *startGate) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	d.once.Do(func() { close(d.reached) })
	select {
	case <-d.release:
	case <-ctx.Done():
	}
	return d.AgentDriver.Start(ctx, spec)
}

func (d *startGate) free() { d.freed.Do(func() { close(d.release) }) }

// await waits for the lane to be held in the start of its session.
func (d *startGate) await(t *testing.T) {
	t.Helper()
	select {
	case <-d.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the lane never started its session")
	}
}

// awaitParked waits until a goroutine of the test, running the function, waits on a lock. The test holds
// that lock, so the goroutine has done what it does before it.
func awaitParked(t *testing.T, function string) {
	t.Helper()
	buffer := make([]byte, 1<<16)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		n := runtime.Stack(buffer, true)
		if n == len(buffer) {
			buffer = make([]byte, 2*len(buffer))
			continue
		}
		for _, goroutine := range strings.Split(string(buffer[:n]), "\n\n") {
			if strings.Contains(goroutine, "[sync.Mutex.Lock") && strings.Contains(goroutine, t.Name()) && strings.Contains(goroutine, function) {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no goroutine of the test running %s waits on a lock", function)
}

// A lane that lost its lease while the driver held no session for it stops that session once it does, and
// the lane ends: the stop waits for the session, as a respawn's does.
func TestALeaseLostBeforeTheSessionIsReachedStopsItOnceItIs(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	r.endsWithTheTest()
	held := newStartGate(r.driver)
	r.config.Driver = held
	t.Cleanup(held.free)
	var stops atomic.Int64
	r.driver.OnStop(func() { stops.Add(1) })
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	defer r.noStopWithoutASession(t)
	r.start()
	held.await(t)
	r.cancelWork(t)
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	held.free()
	r.waitEnded(t)
	r.expectCanceled(t)
	if got := stops.Load(); got != 1 {
		t.Errorf("%d stops reached the session", got)
	}
}

// A lane whose next generation began with no session is not stopped by a lease lost then. The session never
// starts, and the lane ends without a stop of a session the driver lacks.
func TestALeaseLostBetweenTwoGenerationsStartsNoSession(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, restarts)
	r.endsWithTheTest()
	held := r.holdsLookup(2)
	r.driver.Play(script(crashed()))
	defer r.noStopWithoutASession(t)
	r.start()
	r.laneState(lanes.Backoff)
	r.clock.Advance(31 * time.Second)
	held.await(t)
	r.cancelWork(t)
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	held.free()
	r.waitEnded(t)
	r.expectCanceled(t)
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("%d sessions", len(specs))
	}
}

// A pass that found the lease gone reads the generation in pieces, and can stall between two of them while
// the run begins the next generation. What it reads before the stall is the last generation's, so the stop it
// spends has to be the last generation's too: the next generation's is the only one that session has, and a
// stop spent before the driver holds the session finds none.
func TestALeaseLostAsTheRunMovesOnSpendsNoStopOfTheNextGeneration(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	g := newGate()
	r.endsWithTheTest()
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	defer r.noStopWithoutASession(t)
	r.start()
	r.reach(t, g)
	// The run reads the session's stream apart from the stall below, so it has to be there first.
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	crossing := r.subject.HoldGeneration("lane-1")
	t.Cleanup(crossing.Let)
	passed := make(chan struct{})
	go func() {
		defer close(passed)
		r.subject.Lost(t.Context(), "lane-1")
	}()
	awaitParked(t, "(*laneRun).lost(")
	crossing.MoveOn()
	crossing.Let()
	select {
	case <-passed:
	case <-time.After(20 * time.Second):
		t.Fatal("the pass never ended")
	}
	if crossing.NextStopSpent() {
		t.Error("the pass spent the stop of the generation that followed")
	}
	crossing.Restore()
	close(g.release)
	r.driver.StopEverySession()
	r.waitEnded(t)
}
