package worker_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/lexical"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
)

// heldReads holds every read of the driver's capabilities once a test asks. A run reads them first when it
// prepares a session after its wake, so a test reads a run a wake reached that has not prepared yet.
type heldReads struct {
	*scriptedLanes
	held    atomic.Bool
	release chan struct{}
	once    sync.Once
}

func (d *heldReads) Capabilities(ctx context.Context) (ports.Capabilities, error) {
	if d.held.Load() {
		select {
		case <-d.release:
		case <-ctx.Done():
		}
	}
	return d.scriptedLanes.Capabilities(ctx)
}

func (d *heldReads) hold() { d.held.Store(true) }

func (d *heldReads) free() { d.once.Do(func() { close(d.release) }) }

// published waits for the orchestrator's row to read as a client reads it, publishing nothing by hand.
func (o *orchestratorRig) published(t *testing.T, what string, done func(ledger.Orchestrator) bool) {
	t.Helper()
	deadline := time.Now().Add(2*worker.PublishSettle + 20*time.Second)
	for time.Now().Before(deadline) {
		if done(o.row(t)) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s: row %+v", what, o.row(t))
}

// noted waits for a running worker to read what was asked, on a pass of its own.
func (o *orchestratorRig) noted(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if o.subject.WakeWaiting() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%d items noted, want %d", o.subject.WakeWaiting(), want)
}

// An orchestrator with nothing to judge holds its generation before it prepares anything, and its worker says
// so beside the state, so a member reads a wait and not a preparation that never ends. A wake ends the wait
// before the run prepares. The worker beats once an hour and nothing publishes by hand, so only the run's own
// word carries the wait, and its end, to the ledger.
func TestAnOrchestratorSaysItAwaitsAWakeUntilOneArrives(t *testing.T) {
	base := newRig(t)
	base.plane = control.NewWorkers(base.store, scope, control.WorkerOptions{
		HeartbeatMs: time.Hour.Milliseconds(), LeaseTTLMs: 2 * time.Hour.Milliseconds(), Matcher: lexical.Matcher{Store: base.store},
	})
	base.link = &fake.WorkerLink{Plane: base.plane}
	r := newLaneRigFor(t, base)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser(orchestratorProfile)
	ctx, cancel := context.WithCancel(t.Context())
	o := &orchestratorRig{laneRig: r, ctx: ctx, scripts: []fake.DriverScript{aSession()}}
	driver := &heldReads{scriptedLanes: &scriptedLanes{AgentDriver: r.driver, script: o.scriptFor}, release: make(chan struct{})}
	r.config.Driver = driver
	done := make(chan error, 1)
	go func() { done <- r.subject.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		driver.free()
		r.driver.StopEverySession()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("run ended with %v", err)
		}
		r.subject.Wait()
	})
	awaits := func(row ledger.Orchestrator) bool { return slices.Contains(row.Conditions, string(lanes.AwaitingWake)) }

	o.start(t)
	o.published(t, "a run with nothing to judge", func(row ledger.Orchestrator) bool {
		return row.Actual == string(lanes.Provisioning) && awaits(row)
	})
	driver.hold()
	o.asked(t, "q1", "Which retry budget?")
	o.noted(t, 1)
	o.clock.Advance(31 * time.Second)
	o.published(t, "a run a wake reached, before it prepared", func(row ledger.Orchestrator) bool {
		return row.Actual == string(lanes.Provisioning) && !awaits(row)
	})
	driver.free()
	o.published(t, "a run at rest", func(row ledger.Orchestrator) bool {
		return row.Actual == string(lanes.Idle) && !awaits(row)
	})
}
