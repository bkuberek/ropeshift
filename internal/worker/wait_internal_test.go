package worker

import (
	"context"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// heldUploads is a control plane that takes an upload only once the test lets it. It answers nothing else.
type heldUploads struct {
	ports.WorkerLink
	reached chan struct{}
	once    sync.Once
	release chan struct{}
}

func (l *heldUploads) Upload(_ context.Context, batch ledger.UploadBatch) (ledger.UploadReply, error) {
	l.once.Do(func() { close(l.reached) })
	<-l.release
	return ledger.UploadReply{HighWater: batch.Events[len(batch.Events)-1].Seq}, nil
}

// free lets every held upload go on. It is safe to call twice.
func (l *heldUploads) free() {
	select {
	case <-l.release:
	default:
		close(l.release)
	}
}

// TestWaitOutlastsAnUploadInFlight stops a worker while it sends what it just recorded, here a member's
// message reaching its orchestrator. The upload reads and acknowledges the journal after the call that
// started it, so the worker's wait covers it.
func TestWaitOutlastsAnUploadInFlight(t *testing.T) {
	t.Parallel()
	journal, err := fake.NewJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	link := &heldUploads{reached: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(link.free)
	w := New(link, journal, fake.NewClock(time.UnixMilli(1_000_000)), Config{})
	w.noteOrchestratorMessages(t.Context(), []ledger.WakeItem{{Project: "p", Kind: ledger.WakeMessage, ID: "m-1"}})
	select {
	case <-link.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("no upload started")
	}
	if got := w.Unfinished(); !slices.Equal(got, []string{"upload (1)"}) {
		t.Errorf("a worker with an upload on its way names %q", got)
	}
	waited := make(chan struct{})
	go func() {
		defer close(waited)
		w.Wait()
	}()
	select {
	case <-waited:
		t.Fatal("Wait returned while an upload was on its way")
	case <-time.After(100 * time.Millisecond):
	}
	link.free()
	select {
	case <-waited:
	case <-time.After(20 * time.Second):
		t.Fatal("Wait never returned once the upload ended")
	}
	pending, err := journal.Pending(t.Context(), 10)
	if err != nil || len(pending) != 0 {
		t.Errorf("the journal still holds %d events to send: %v", len(pending), err)
	}
}

// pipeListener hands ServeLanes the far ends of in-memory connections a test dials.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.UnixAddr{Name: "pipe", Net: "unix"} }

// dial connects as a hook does, and returns once the listener has taken the connection.
func (l *pipeListener) dial(t *testing.T) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	select {
	case l.conns <- server:
	case <-time.After(20 * time.Second):
		t.Fatal("the socket never took the connection")
	}
	return client
}

// TestWaitOutlastsASocketCallInFlight stops a worker while a hook's connection has not sent its line. Its
// handler answers the line by writing the journal and reaching the control plane, so the worker's wait
// covers the handler even once the socket takes no more connections.
func TestWaitOutlastsASocketCallInFlight(t *testing.T) {
	t.Parallel()
	w := New(nil, nil, fake.NewClock(time.UnixMilli(1_000_000)), Config{})
	listener := newPipeListener()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- w.ServeLanes(ctx, listener) }()
	client := listener.dial(t)
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the socket never stopped")
	}
	if got := w.Unfinished(); !slices.Equal(got, []string{"socket call (1)"}) {
		t.Errorf("a worker with a socket call in flight names %q", got)
	}
	waited := make(chan struct{})
	go func() {
		defer close(waited)
		w.Wait()
	}()
	select {
	case <-waited:
		t.Fatal("Wait returned while a socket call was in flight")
	case <-time.After(100 * time.Millisecond):
	}
	_ = client.Close()
	select {
	case <-waited:
	case <-time.After(20 * time.Second):
		t.Fatal("Wait never returned once the socket call ended")
	}
	if got := w.Unfinished(); len(got) != 0 {
		t.Errorf("a worker whose wait is over names %q", got)
	}
}
