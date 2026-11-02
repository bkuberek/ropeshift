package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/apitest"
)

const streamWait = 30 * time.Second

func revisionOf(t *testing.T, d *apitest.Daemon) int64 {
	t.Helper()
	health, err := d.Client.Health(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return health.Revision
}

func note(t *testing.T, d *apitest.Daemon, id string) {
	t.Helper()
	err := d.Store.Write(t.Context(), apitest.Scope, "test.note", func(tx ports.WriteTx) error {
		_, err := tx.Append(ledger.EventDraft{
			Type: "test.note", Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}, Subject: ledger.Subject{Kind: "note", ID: id},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// readEvents takes events until it holds n or the wait runs out.
func readEvents(t *testing.T, events <-chan api.StreamEvent, n int) []api.StreamEvent {
	t.Helper()
	var out []api.StreamEvent
	timeout := time.After(streamWait)
	for len(out) < n {
		select {
		case event, open := <-events:
			if !open {
				t.Fatalf("the stream ended after %d of %d events", len(out), n)
			}
			out = append(out, event)
		case <-timeout:
			t.Fatalf("%d of %d events arrived", len(out), n)
		}
	}
	return out
}

func gapless(t *testing.T, events []api.StreamEvent, after int64) {
	t.Helper()
	for i, event := range events {
		if want := after + int64(i) + 1; event.Rev != want {
			t.Fatalf("event %d is revision %d, want %d", i, event.Rev, want)
		}
	}
}

func TestAStreamCarriesEveryEventInOrder(t *testing.T) {
	d := apitest.Start(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	start := revisionOf(t, d)
	note(t, d, "before")
	events, err := d.Client.Events(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	note(t, d, "during")
	got := readEvents(t, events, 2)
	gapless(t, got, start)
	if got[0].SubjectID != "before" || got[1].SubjectID != "during" || got[0].Type != "test.note" {
		t.Errorf("events %+v", got)
	}
	if got[0].RecordedAt == 0 || got[0].ActorKind != string(ledger.ActorSystem) {
		t.Errorf("event %+v", got[0])
	}
}

// A reader that loses its connection comes back with the last revision it saw, and whatever happened
// while it was away arrives, with nothing missed and nothing twice.
func TestAStreamResumesFromLastEventIDAndMissesNothing(t *testing.T) {
	d := apitest.Start(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	start := revisionOf(t, d)
	first, err := d.Client.Events(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	note(t, d, "one")
	note(t, d, "two")
	got := readEvents(t, first, 2)
	cancel()

	note(t, d, "while away")
	note(t, d, "and again")
	again, stop := context.WithCancel(t.Context())
	defer stop()
	second, err := d.Client.Events(again, got[len(got)-1].Rev)
	if err != nil {
		t.Fatal(err)
	}
	note(t, d, "after")
	rest := readEvents(t, second, 3)
	gapless(t, append(got, rest...), start)
}

func TestAStreamHoldsMoreThanOnePage(t *testing.T) {
	d := apitest.Start(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	start := revisionOf(t, d)
	const many = 250
	for range many {
		note(t, d, "n")
	}
	events, err := d.Client.Events(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	gapless(t, readEvents(t, events, many), start)
}

func TestAStreamRefusesAResumePointThatIsNoRevision(t *testing.T) {
	d := apitest.Start(t)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://ropeshift"+api.EventsPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", "not a revision")
	response, err := apitest.RawClient(d.Socket).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d", response.StatusCode)
	}
}

// What moves a lane reaches a reader as a record's name, and the record stays behind a view.
func TestAStreamNamesWhatChangedAndCarriesNoContent(t *testing.T) {
	d := apitest.Start(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events, err := d.Client.Events(ctx, revisionOf(t, d))
	if err != nil {
		t.Fatal(err)
	}
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if _, err := control.NewWorkers(d.Store, apitest.Scope, control.WorkerOptions{}).CreateJoinToken(ctx, member); err != nil {
		t.Fatal(err)
	}
	got := readEvents(t, events, 1)[0]
	if got.Type == "" || got.SubjectID == "" {
		t.Errorf("event %+v", got)
	}
}
