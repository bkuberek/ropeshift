package entry

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// heldWorker stands for a worker one of whose goroutines ignores the end of its context: its wait returns
// only when the test lets that goroutine go.
type heldWorker struct {
	release chan struct{}
	once    sync.Once
	running []string
	// began closes when a wait begins.
	began     chan struct{}
	beganOnce sync.Once
}

func newHeldWorker(running ...string) *heldWorker {
	return &heldWorker{release: make(chan struct{}), running: running, began: make(chan struct{})}
}

func (w *heldWorker) Wait() {
	w.beganOnce.Do(func() { close(w.began) })
	<-w.release
}

func (w *heldWorker) Unfinished() []string { return w.running }
func (w *heldWorker) free()                { w.once.Do(func() { close(w.release) }) }

// TestAStoppingDaemonClosesItsWorkerAfterTheGrace bounds a daemon's wait on its worker. A goroutine that
// never ends must not hold the journal and link open until a service manager kills the daemon, so the wait
// gives up after the grace and says what it left running.
func TestAStoppingDaemonClosesItsWorkerAfterTheGrace(t *testing.T) {
	t.Parallel()
	const grace = 50 * time.Millisecond
	held := newHeldWorker("forge request wo-2", "lane lane-1")
	t.Cleanup(held.free)
	var log strings.Builder
	returned := make(chan time.Duration, 1)
	began := time.Now()
	go func() {
		awaitWorker(held, grace, &log)
		returned <- time.Since(began)
	}()
	select {
	case took := <-returned:
		if took < grace {
			t.Errorf("the wait gave up after %s, before its grace of %s", took, grace)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait never gave up on a worker that does not end")
	}
	want := "ropeshiftd: worker: still running after 50ms, closing its journal and link anyway: forge request wo-2, lane lane-1\n"
	if got := log.String(); got != want {
		t.Errorf("the daemon said %q, want %q", got, want)
	}
}

// TestAStoppingDaemonClosesItsWorkerOnceItsWorkEnds waits no longer than the worker's work takes, and says
// nothing of a stop that went as it should.
func TestAStoppingDaemonClosesItsWorkerOnceItsWorkEnds(t *testing.T) {
	t.Parallel()
	held := newHeldWorker("lane lane-1")
	var log strings.Builder
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		awaitWorker(held, time.Hour, &log)
	}()
	held.free()
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("the wait went on after the worker's work ended")
	}
	if got := log.String(); got != "" {
		t.Errorf("a stop that went as it should said %q", got)
	}
}

// TestAStoppingDaemonWaitsOnItsWorkerOnceTheSocketStops begins the worker's wait only after the socket takes
// no more connections. A connection starts a goroutine that wait covers, and one started after the wait
// began would go on past it, onto a closed journal.
func TestAStoppingDaemonWaitsOnItsWorkerOnceTheSocketStops(t *testing.T) {
	t.Parallel()
	held := newHeldWorker()
	held.free()
	socketDone := make(chan struct{})
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		stopWorker(held, socketDone, time.Hour, io.Discard)
	}()
	select {
	case <-held.began:
		t.Fatal("the worker's wait began while the socket could still take connections")
	case <-time.After(100 * time.Millisecond):
	}
	close(socketDone)
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("the stop never ended once the socket stopped")
	}
	select {
	case <-held.began:
	default:
		t.Error("the stop never waited on the worker")
	}
}

// failedWorker stands for a worker whose loop ends on its own, as a refused registration ends it, and whose
// socket serves until its context ends, as the worker's does. What it started has ended.
type failedWorker struct {
	*heldWorker
}

func (failedWorker) Run(context.Context) error {
	return errors.New("the control plane refused this worker")
}

func (failedWorker) ServeLanes(ctx context.Context, _ net.Listener) error {
	<-ctx.Done()
	return nil
}

// TestAWorkerWhoseLoopEndsOnItsOwnStillStops runs a worker whose loop fails while the daemon's context goes
// on. The socket stops with the loop, so the stop's wait on it ends, and the worker's journal and link close.
func TestAWorkerWhoseLoopEndsOnItsOwnStillStops(t *testing.T) {
	t.Parallel()
	held := newHeldWorker()
	held.free()
	var closed atomic.Bool
	var log strings.Builder
	done := superviseWorker(t.Context(), failedWorker{held}, nil, func() { closed.Store(true) }, &log)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never stopped after its loop ended: its socket still serves")
	}
	if !closed.Load() {
		t.Error("the worker's journal and link stayed open after its loop ended")
	}
	if want := "ropeshiftd: worker: the control plane refused this worker\n"; log.String() != want {
		t.Errorf("the daemon said %q, want %q", log.String(), want)
	}
}
