package worker_test

import (
	"context"
	"errors"
	"strings"
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

// keepMoving moves the lane into `compacting` and back out each round until span runs out, each move far
// inside one window, and asks seen between moves. The lane reads `compacting` for nearly all of each round,
// so a publish at any moment almost always finds it there, and the last move leaves it `working`. It returns
// how long after the first move seen said yes, and whether it did before span ran out.
func (r *laneRig) keepMoving(t *testing.T, span time.Duration, seen func() bool) (time.Duration, bool) {
	t.Helper()
	const compacting, working = 290 * time.Millisecond, 10 * time.Millisecond
	began := time.Now()
	for time.Since(began) < span {
		r.hook(t, lanes.HookEvent{Event: "PreCompact", Subject: "s", Detail: "auto"})
		for held := time.Now(); time.Since(held) < compacting; time.Sleep(10 * time.Millisecond) {
			if seen() {
				return time.Since(began), true
			}
		}
		r.hook(t, lanes.HookEvent{Event: "PostCompact", Subject: "s", Detail: "auto"})
		time.Sleep(working)
	}
	return time.Since(began), false
}

// TestALaneThatKeepsMovingStillPublishes holds a publish to one window from a lane's first move: a later move
// does not put the window off, so a lane that moves more often than once a window is seen while it moves, and
// not only once it stops. A window that closes in the moment the lane is between moves finds nothing new, and
// a loaded machine runs late, so the lane moves for three windows.
func TestALaneThatKeepsMovingStillPublishes(t *testing.T) {
	r, _ := runBeatingRarely(t)
	r.awaitWorking(t, 2*worker.PublishSettle+5*time.Second)
	compacting := func() bool {
		view, err := r.plane.Lane(t.Context(), "lane-1")
		if err != nil {
			t.Fatal(err)
		}
		return view.ActualState == string(lanes.Compacting)
	}
	if moved, seen := r.keepMoving(t, 3*worker.PublishSettle, compacting); !seen {
		t.Fatalf("a lane that moved for %s was never published while it moved", moved)
	}
}

// TestAnAwayPlaneHearsOneFailedPublishPerCycle keeps a worker whose control plane is away from reporting a
// failure every window while a lane keeps moving: the first publish that fails leaves the rest to the next
// cycle, which tries again after its own retry wait. The heartbeat is an hour off, so the lane's moves for
// four windows, and the window its last move opens, report one failure.
func TestAnAwayPlaneHearsOneFailedPublishPerCycle(t *testing.T) {
	r, _ := runBeatingRarely(t)
	r.awaitWorking(t, 2*worker.PublishSettle+5*time.Second)
	r.link.FailUpload.Store(true)
	before := len(r.problems())
	r.keepMoving(t, 4*worker.PublishSettle, func() bool { return false })
	time.Sleep(worker.PublishSettle + 500*time.Millisecond)
	var failed []error
	for _, problem := range r.problems()[before:] {
		if strings.Contains(problem.Error(), "the control plane is away") {
			failed = append(failed, problem)
		}
	}
	if len(failed) != 1 {
		t.Errorf("a lane that moved for four windows while the plane was away reported %d failed publishes: %v", len(failed), failed)
	}
}
