package worker_test

import (
	"context"
	"sync"
	"testing"

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
	r.stopWorker()
	r.subject.Wait()
	if change := r.outcome(); change.Reason == workorders.ReasonSpawnFailed {
		t.Errorf("the lane ended with %+v", change)
	}
	if row := r.row(); row.State != workorders.Running {
		t.Errorf("the work order reads %s, want it still running, held by a lane the next worker starts", row.State)
	}
}
