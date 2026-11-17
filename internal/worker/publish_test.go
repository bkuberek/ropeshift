package worker_test

import (
	"context"
	"errors"
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

// laneStatuses counts the lane statuses a worker journals. Each is one status an upload sends.
type laneStatuses struct {
	ports.Journal
	count atomic.Int64
}

func (j *laneStatuses) Append(ctx context.Context, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	if event.Type == ledger.UploadLaneStatus {
		j.count.Add(1)
	}
	return j.Journal.Append(ctx, event)
}

// runBeatingRarely runs a worker whose plane asks for a beat once an hour and whose hints never arrive, so
// nothing but the lane's own moves can send its status while the test runs. The lane starts and holds at a
// gate in its first turn.
func runBeatingRarely(t *testing.T) (*laneRig, *laneStatuses) {
	t.Helper()
	base := newRig(t)
	base.plane = control.NewWorkers(base.store, scope, control.WorkerOptions{
		HeartbeatMs: time.Hour.Milliseconds(), LeaseTTLMs: 2 * time.Hour.Milliseconds(), Matcher: lexical.Matcher{Store: base.store},
	})
	base.link = &fake.WorkerLink{Plane: base.plane}
	base.link.DropHints.Store(true)
	statuses := &laneStatuses{Journal: base.journal}
	base.workerJournal = statuses
	r := newLaneRigFor(t, base)
	g := newGate()
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- r.subject.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		g.free()
		r.driver.StopEverySession()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("run ended with %v", err)
		}
		r.waitForTheLane(t)
	})
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached its gate")
	}
	return r, statuses
}

// awaitWorking reads the lane the way a client does, publishing nothing by hand, until it reads `working`.
func (r *laneRig) awaitWorking(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		view, err := r.plane.Lane(t.Context(), "lane-1")
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			t.Fatal(err)
		}
		if view.ActualState == string(lanes.Working) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %s the lane reads %+v", within, view)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestALaneStateChangeReachesTheLedgerBeforeTheHeartbeat keeps a lane's status from waiting on the next
// heartbeat: a lane that moves wakes its worker, which publishes once the moves settle. The session runs
// ahead of the worker that follows it, so its last move can open a second window, and the margin covers a
// loaded machine; both are far short of the hour the heartbeat would take.
func TestALaneStateChangeReachesTheLedgerBeforeTheHeartbeat(t *testing.T) {
	r, _ := runBeatingRarely(t)
	r.awaitWorking(t, 2*worker.PublishSettle+5*time.Second)
}

// TestABurstOfLaneMovesPublishesOnce keeps a lane's start, which moves it from `pending` through
// `provisioning`, `starting`, and `briefing` to `working` at once, to one status: the worker waits for the
// moves to settle and sends where they ended. Every status the worker journaled was uploaded before the
// lane read `working`.
func TestABurstOfLaneMovesPublishesOnce(t *testing.T) {
	r, statuses := runBeatingRarely(t)
	r.awaitWorking(t, 2*worker.PublishSettle+5*time.Second)
	if published := statuses.count.Load(); published != 1 {
		t.Errorf("one burst of moves published %d statuses", published)
	}
}
