package worker_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
)

// watchedAdoption is a driver that takes a session over only when its worker's first probe has ended and been handed
// to it, the way a driver's adoption reads what qualifies the session's version when it runs, and that says when the
// worker went on to read the adopted session, which is after the lane noted what the adoption used.
type watchedAdoption struct {
	ports.AgentDriver
	// until is what the adoption waits for.
	until <-chan struct{}
	// handed closes when the driver is first handed a probe's result, and read when the lane reads the session's events.
	handed, read         chan struct{}
	handedOnce, readOnce sync.Once
}

func newWatchedAdoption(driver ports.AgentDriver, waitForProbe bool) *watchedAdoption {
	d := &watchedAdoption{AgentDriver: driver, handed: make(chan struct{}), read: make(chan struct{})}
	if waitForProbe {
		d.until = d.handed
	} else {
		now := make(chan struct{})
		close(now)
		d.until = now
	}
	return d
}

func (d *watchedAdoption) Qualify(q lanes.Qualification) {
	d.AgentDriver.Qualify(q)
	d.handedOnce.Do(func() { close(d.handed) })
}

func (d *watchedAdoption) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	select {
	case <-d.until:
	case <-ctx.Done():
		return ctx.Err()
	}
	return d.AgentDriver.Adopt(ctx, h, from)
}

func (d *watchedAdoption) Events(ctx context.Context, h ports.SessionHandle) (<-chan ports.DriverEvent, error) {
	d.readOnce.Do(func() { close(d.read) })
	return d.AgentDriver.Events(ctx, h)
}

// aMatrixLaneTakenOver is a lane a worker with no probe history started on a version the shipped matrix holds, whose
// session a new worker finds while that worker's own first probe is still running. The first worker's probe never
// ends, so the journal holds no result, and the new worker's probe ends when the returned function runs.
func aMatrixLaneTakenOver(t *testing.T, waitForProbe bool) (r *laneRig, taken *watchedAdoption, driver *fake.AgentDriver, endProbe func()) {
	t.Helper()
	var held func()
	g := newGate()
	r = newLaneRigOn(t, func(first *fake.AgentDriver) {
		first.SetVersion("1.0.0", false)
		held = first.HoldProbes()
	})
	t.Cleanup(held)
	r.dispatched()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.start()
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached its gate")
	}
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })

	first := r.subject
	r.stopWorker()
	t.Cleanup(func() {
		r.driver.StopEverySession()
		first.Wait()
	})
	driver = fake.NewAgentDriver()
	driver.SetVersion("1.0.0", false)
	driver.LeaveOn("lane-1", 1, lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true}, "1.0.0")
	plays := script([]fake.DriverStep{{Hold: true}})
	plays.Silent = true
	driver.Play(plays)
	endProbe = driver.HoldProbes()
	t.Cleanup(endProbe)
	taken = newWatchedAdoption(driver, waitForProbe)
	// The first worker journaled the probe it began, and a worker that starts holds its own back for a while after it.
	r.clock.Advance(restartedAfter)
	config := *r.config
	config.Driver = taken
	r.subject = worker.New(r.link, r.journal, r.clock, worker.Config{Name: "test", Lanes: &config})
	t.Cleanup(func() {
		driver.StopEverySession()
		r.subject.Wait()
		endSessions(t, driver)
	})
	if err := r.subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	return r, taken, driver, endProbe
}

// awaitRead waits for the lane to go on to read the session it adopted.
func (d *watchedAdoption) awaitRead(t *testing.T) {
	t.Helper()
	select {
	case <-d.read:
	case <-time.After(20 * time.Second):
		t.Fatal("the lane never read the session it adopted")
	}
}

// A driver answers from the shipped matrix only while it holds no probe's result, so a session that a worker takes
// over after its first probe ended runs on that probe's answer, whatever the matrix said when the worker looked for
// it. The lane then says nothing of the matrix's surfaces, whichever way the probe went.
func TestAnAdoptionAfterTheFirstProbeEndedDoesNotClaimTheMatrixSurfaces(t *testing.T) {
	t.Parallel()
	r, taken, driver, endProbe := aMatrixLaneTakenOver(t, true)
	driver.SetVersion("1.0.0", true)
	endProbe()
	select {
	case <-taken.handed:
	case <-time.After(20 * time.Second):
		t.Fatal("the probe never ended")
	}
	taken.awaitRead(t)
	view := r.read()
	if !slices.Contains(view.Conditions, string(lanes.DegradedMode)) {
		t.Errorf("a lane whose probe failed reads %v", view.Conditions)
	}
	if view.Surfaces == ledger.SurfacesShipped {
		t.Errorf("the lane claims the shipped matrix's surfaces, which its session was taken over without")
	}
	if note := view.ConditionNotes[string(lanes.DegradedMode)]; strings.Contains(note, "this release qualified") {
		t.Errorf("the lane says %q", note)
	}
	if view.Checking != nil {
		t.Errorf("the lane names a check after its probe ended: %+v", view.Checking)
	}
}

// A session a worker takes over while no probe has ended ran on the shipped matrix, and the lane says so.
func TestAnAdoptionBeforeAnyProbeEndedKeepsTheMatrixSurfaces(t *testing.T) {
	t.Parallel()
	r, taken, _, _ := aMatrixLaneTakenOver(t, false)
	taken.awaitRead(t)
	view := r.read()
	if view.Surfaces != ledger.SurfacesShipped {
		t.Errorf("the lane says its surfaces come from %q, want %q", view.Surfaces, ledger.SurfacesShipped)
	}
	if want := ledger.CheckingCLI(fake.FakeDriverName, "1.0.0", ledger.SurfacesShipped); view.Checking == nil || view.Checking.Words != want.Words {
		t.Errorf("the lane names %+v, want %q", view.Checking, want.Words)
	}
	if slices.Contains(view.Conditions, string(lanes.DegradedMode)) {
		t.Errorf("a lane on the matrix's surfaces reads %v", view.Conditions)
	}
}
