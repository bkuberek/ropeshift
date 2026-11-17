package worker

import (
	"context"
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
