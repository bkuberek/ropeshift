package worker_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// adoptHeld is a driver whose adoption of a session waits until a test lets it go, as one does that reads a pane. It
// counts what is sent to the session or asked to stop it while that adoption waits, because the driver holds no session
// then.
type adoptHeld struct {
	ports.AgentDriver
	reached chan struct{}
	release chan struct{}
	opened  sync.Once
	freed   sync.Once
	early   atomic.Int64
	adopted atomic.Bool
	// fail is what the adoption returns, in place of adopting, once a test lets it go.
	fail error
}

func (d *adoptHeld) Send(ctx context.Context, handle ports.SessionHandle, in ports.Input) (ports.SendReceipt, error) {
	if !d.adopted.Load() {
		d.early.Add(1)
	}
	return d.AgentDriver.Send(ctx, handle, in)
}

func (d *adoptHeld) Stop(ctx context.Context, handle ports.SessionHandle, how ports.StopMode) error {
	if !d.adopted.Load() {
		d.early.Add(1)
	}
	return d.AgentDriver.Stop(ctx, handle, how)
}

func newAdoptHeld(driver ports.AgentDriver) *adoptHeld {
	return &adoptHeld{AgentDriver: driver, reached: make(chan struct{}), release: make(chan struct{})}
}

func (d *adoptHeld) Adopt(ctx context.Context, handle ports.SessionHandle, from ports.AdoptFrom) error {
	d.opened.Do(func() { close(d.reached) })
	select {
	case <-d.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	if d.fail != nil {
		return d.fail
	}
	err := d.AgentDriver.Adopt(ctx, handle, from)
	d.adopted.Store(true)
	return err
}

func (d *adoptHeld) let() { d.freed.Do(func() { close(d.release) }) }

// anAdoptionInProgress leaves a lane at rest whose session a restarted worker is adopting, and holds the adoption in the
// driver. setup says what else the journal holds of the lane.
func anAdoptionInProgress(t *testing.T, setup func(*worldRig)) (*worldRig, *adoptHeld) {
	t.Helper()
	w := newWorldRig(t)
	held := newAdoptHeld(w.driver)
	t.Cleanup(held.let)
	w.config.Driver = held
	w.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", "[lifecycle]\nhandoff_timeout = \"30ms\"\n")
	w.desire("lane-1")
	w.record("lane-1", lanes.StateRunning)
	if err := w.journal.SaveActual(t.Context(), "lane-1", lanes.Idle, ""); err != nil {
		t.Fatal(err)
	}
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.driver.Play(script([]fake.DriverStep{{Hold: true}}))
	setup(w)
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-held.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the worker never began to adopt the session")
	}
	return w, held
}

// noEarlyCalls says the park's sequence has asked nothing of the session while its adoption waits. The sequence runs on
// a goroutine of its own, so the check gives it time to begin.
func (d *adoptHeld) noEarlyCalls(t *testing.T) {
	t.Helper()
	time.Sleep(20 * superviseStep)
	if got := d.early.Load(); got != 0 {
		t.Fatalf("the park's sequence sent or stopped %d times at a session the driver did not hold yet", got)
	}
}

// awaitParked lets the adoption go and passes until the lane's park is kept, moving the clock as a wait on the session's
// narrative needs. A park that hangs leaves the lane where it was.
func (w *worldRig) awaitParked(t *testing.T, held *adoptHeld) {
	t.Helper()
	held.let()
	deadline := time.Now().Add(30 * time.Second)
	for w.lane("lane-1").State != lanes.StateParked {
		if time.Now().After(deadline) {
			t.Fatalf("the lane never parked: %+v", w.lane("lane-1"))
		}
		w.clock.Advance(time.Second)
		if _, err := w.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(superviseStep)
	}
}

// A drain that parks a lane while its session is being adopted waits for the adoption: the sequence's stop would go to a
// session the driver does not hold yet. The park finishes once the adoption has.
func TestADrainParkDuringAnAdoptionFinishes(t *testing.T) {
	t.Parallel()
	w, held := anAdoptionInProgress(t, func(*worldRig) {})
	if err := w.journal.SaveDesired(t.Context(), ledger.DesiredState{Rev: 2, FleetMode: ledger.FleetDrain, Leases: w.leases}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if record := w.lane("lane-1"); !record.Parking.Open() {
		t.Fatalf("a draining fleet did not begin the lane's park: %+v", record.Parking)
	}
	held.noEarlyCalls(t)
	w.awaitParked(t, held)
}

// A park a long wait asks for, begun while the session is being adopted, finishes after the adoption as a drain's does.
func TestAWaitParkDuringAnAdoptionFinishes(t *testing.T) {
	t.Parallel()
	w, held := anAdoptionInProgress(t, func(w *worldRig) {
		if err := w.journal.SaveWaitPark(t.Context(), "lane-1", w.clock.Now().Add(30*time.Minute).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if record := w.lane("lane-1"); !record.Parking.Open() {
		t.Fatalf("a long wait did not begin the lane's park: %+v", record.Parking)
	}
	held.noEarlyCalls(t)
	w.awaitParked(t, held)
}

// A park begun while the session is being adopted leaves the handoff it must finish in the run, which the adoption reads when
// it fails, on its own goroutine. The park's goroutine and the adoption's share it under the flight's lock, and no test
// orders them: the adoption fails after the park begun, and nothing but the lock stands between the two.
func TestAParkBegunDuringAnAdoptionThatFailsIsReadUnderTheLock(t *testing.T) {
	t.Parallel()
	w, held := anAdoptionInProgress(t, func(*worldRig) {})
	begun := make(chan struct{})
	go func() {
		defer close(begun)
		request := lanes.ParkRequest{Trigger: lanes.ParkBlockingQuestion, Expr: lanes.HumanWake(), SkipNarrative: true}
		if err := w.subject.Park(t.Context(), "lane-1", request); err != nil {
			t.Errorf("the park was refused: %v", err)
		}
	}()
	// Time and not a signal lets the park's write come first, so that a read of it with no lock has nothing to order it.
	time.Sleep(300 * time.Millisecond)
	held.fail = errors.New("the pane could not be read")
	w.awaitParked(t, held)
	<-begun
}
