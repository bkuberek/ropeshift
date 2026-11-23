package worker_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// launchHeld is a driver whose start waits until the worker's context ends and then fails with its error, as a start
// does that a stopping daemon cuts off.
type launchHeld struct {
	ports.AgentDriver
	once    sync.Once
	reached chan struct{}
}

func (d *launchHeld) Start(ctx context.Context, _ ports.SessionSpec) (ports.SessionHandle, error) {
	d.once.Do(func() { close(d.reached) })
	<-ctx.Done()
	return ports.SessionHandle{}, ctx.Err()
}

// A lane whose session was being started when the daemon stopped is not charged a failed start: the worker is going
// away, so the lane ends with no outcome and the next worker starts it again from its journal, with no attempt used.
func TestALaneProvisioningWhenTheDaemonStopsIsNotChargedAFailure(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, noRestarts)
	held := &launchHeld{AgentDriver: r.driver, reached: make(chan struct{})}
	r.config.Driver = held
	r.start()
	<-held.reached
	attempts := r.row().Attempts
	r.stopWorker()
	r.subject.Wait()
	if change := r.outcome(); change.Reason == workorders.ReasonSpawnFailed {
		t.Errorf("the lane ended with %+v", change)
	}
	if row := r.row(); row.State != workorders.Running {
		t.Errorf("the work order reads %s, want it still running, held by a lane the next worker starts", row.State)
	}

	// The next worker on the same journal starts the lane again, and the stop used no attempt.
	next := r.nextWorker(t)
	driver := r.config.Driver.(*fake.AgentDriver)
	driver.Play(script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	if _, err := next.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for len(driver.Specs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(driver.Specs()) != 1 {
		t.Fatalf("the next worker started %d sessions, want the lane started again: the order reads %+v", len(driver.Specs()), r.row())
	}
	if row := r.row(); row.State != workorders.Running || row.Attempts != attempts {
		t.Errorf("the work order reads %s with %d attempts, want running with %d", row.State, row.Attempts, attempts)
	}
	driver.StopEverySession()
	next.Wait()
}

// A stop that lands while the worker sets the lane up, before its session starts, is no failed start either: the
// checkout the setup reads fails because the daemon is stopping, and the lane is left for the next worker.
func TestALaneSetUpWhenTheDaemonStopsIsNotChargedAFailure(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, noRestarts)
	var calls atomic.Int64
	checkout := r.config.Repository
	r.config.Repository = func(ctx context.Context, project, repository string) (string, error) {
		// The first read is the admission's, and the second is the setup's, where the daemon stops.
		if calls.Add(1) == 1 {
			return checkout(ctx, project, repository)
		}
		r.stopWorker()
		return "", errors.New("the checkout cannot be read while the daemon stops")
	}
	r.start()
	r.subject.Wait()
	if calls.Load() < 2 {
		t.Fatalf("the setup read %d checkouts, want the stop to land in it", calls.Load())
	}
	if change := r.outcome(); change.Reason == workorders.ReasonNoRepository || change.Reason == workorders.ReasonSpawnFailed {
		t.Errorf("the lane ended with %+v", change)
	}
	if row := r.row(); row.State != workorders.Running {
		t.Errorf("the work order reads %s, want it still running", row.State)
	}
}
