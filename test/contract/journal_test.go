package contract

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

// TestJournalContract runs one suite on the file and on the fake, which is the same
// adapter on an in-memory database (D6).
func TestJournalContract(t *testing.T) {
	backends := []struct {
		name string
		open func(*testing.T) ports.Journal
	}{
		{"file", func(t *testing.T) ports.Journal {
			journal, err := sqlite.OpenJournal(filepath.Join(t.TempDir(), "journal.db"))
			if err != nil {
				t.Fatal(err)
			}
			return journal
		}},
		{"fake", func(t *testing.T) ports.Journal {
			journal, err := fake.NewJournal()
			if err != nil {
				t.Fatal(err)
			}
			return journal
		}},
	}
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, ports.Journal)
	}{
		{"an empty journal has no identity", checkJournalEmpty},
		{"an identity round trips", checkJournalIdentity},
		{"sequence numbers are contiguous", checkJournalSequence},
		{"racing appends lose nothing", checkJournalRace},
		{"pending stops at the high-water mark", checkJournalPending},
		{"the high-water mark never moves back", checkJournalMark},
		{"a repeated answer keeps one rejection", checkJournalRejections},
		{"leases are replaced whole", checkJournalLeases},
		{"desired state round trips", checkJournalDesired},
		{"a migration repeats as a no-op", checkJournalMigrate},
	}
	for _, backend := range backends {
		for _, check := range checks {
			t.Run(backend.name+"/"+check.name, func(t *testing.T) {
				journal := backend.open(t)
				t.Cleanup(func() { _ = journal.Close() })
				if err := journal.Migrate(t.Context()); err != nil {
					t.Fatal(err)
				}
				check.run(t, t.Context(), journal)
			})
		}
	}
}

func checkJournalEmpty(t *testing.T, ctx context.Context, j ports.Journal) {
	if _, err := j.Identity(ctx); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("identity: %v", err)
	}
	if _, err := j.Desired(ctx); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("desired: %v", err)
	}
	through, err := j.Through(ctx)
	if err != nil || through != 0 {
		t.Errorf("through %d: %v", through, err)
	}
}

func checkJournalIdentity(t *testing.T, ctx context.Context, j ports.Journal) {
	want := ledger.WorkerIdentity{WorkerID: "w", Credential: "c", Epoch: 3}
	if err := j.SaveIdentity(ctx, want); err != nil {
		t.Fatal(err)
	}
	want.Epoch = 4
	if err := j.SaveIdentity(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := j.Identity(ctx)
	if err != nil || got != want {
		t.Errorf("identity %+v: %v", got, err)
	}
}

func checkJournalSequence(t *testing.T, ctx context.Context, j ports.Journal) {
	for want := int64(1); want <= 3; want++ {
		event, err := j.Append(ctx, ledger.UploadEvent{
			Epoch: 2, Type: "note", WorkOrder: "wo", ClaimID: "c", Fence: 4, Lane: "l", Generation: 1,
			Payload: []byte("body"), OccurredAt: 1000,
		})
		if err != nil {
			t.Fatal(err)
		}
		if event.Seq != want {
			t.Errorf("seq %d, want %d", event.Seq, want)
		}
	}
	pending, err := j.Pending(ctx, 10)
	if err != nil || len(pending) != 3 {
		t.Fatalf("%d pending: %v", len(pending), err)
	}
	got := pending[2]
	if got.Epoch != 2 || got.WorkOrder != "wo" || got.ClaimID != "c" || got.Fence != 4 || got.Lane != "l" ||
		got.Generation != 1 || string(got.Payload) != "body" || got.OccurredAt != 1000 {
		t.Errorf("event %+v", got)
	}
}

func checkJournalRace(t *testing.T, ctx context.Context, j ports.Journal) {
	const writers = 8
	var wg sync.WaitGroup
	seqs := make([]int64, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event, err := j.Append(ctx, ledger.UploadEvent{Type: "note", OccurredAt: 1})
			if err != nil {
				t.Error(err)
			}
			seqs[i] = event.Seq
		}()
	}
	wg.Wait()
	slices.Sort(seqs)
	for i, seq := range seqs {
		if seq != int64(i+1) {
			t.Fatalf("seqs %v", seqs)
		}
	}
}

func checkJournalPending(t *testing.T, ctx context.Context, j ports.Journal) {
	for range 5 {
		if _, err := j.Append(ctx, ledger.UploadEvent{Type: "note", OccurredAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := j.Pending(ctx, 2)
	if err != nil || len(first) != 2 || first[0].Seq != 1 {
		t.Fatalf("first %+v: %v", first, err)
	}
	if err := j.Acknowledge(ctx, 3, nil); err != nil {
		t.Fatal(err)
	}
	rest, err := j.Pending(ctx, 10)
	if err != nil || len(rest) != 2 || rest[0].Seq != 4 {
		t.Fatalf("rest %+v: %v", rest, err)
	}
}

func checkJournalMark(t *testing.T, ctx context.Context, j ports.Journal) {
	if err := j.Acknowledge(ctx, 5, nil); err != nil {
		t.Fatal(err)
	}
	if err := j.Acknowledge(ctx, 3, nil); err != nil {
		t.Fatal(err)
	}
	through, err := j.Through(ctx)
	if err != nil || through != 5 {
		t.Errorf("through %d: %v", through, err)
	}
}

func checkJournalRejections(t *testing.T, ctx context.Context, j ports.Journal) {
	result := ledger.UploadResult{Seq: 2, Status: ledger.UploadFenced, CurrentFence: 9, State: "ready"}
	for range 2 {
		if err := j.Acknowledge(ctx, 2, []ports.JournalResult{result}); err != nil {
			t.Fatal(err)
		}
	}
	rejected, err := j.Rejected(ctx)
	if err != nil || len(rejected) != 1 || rejected[0] != result {
		t.Errorf("rejected %+v: %v", rejected, err)
	}
}

func checkJournalLeases(t *testing.T, ctx context.Context, j ports.Journal) {
	first := []ledger.HeldLease{{WorkOrder: "a", Claim: "c1", Fence: 1}, {WorkOrder: "b", Claim: "c2", Fence: 2}}
	if err := j.SaveLeases(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := j.SaveLeases(ctx, first[1:]); err != nil {
		t.Fatal(err)
	}
	got, err := j.Leases(ctx)
	if err != nil || !slices.Equal(got, first[1:]) {
		t.Errorf("leases %+v: %v", got, err)
	}
}

func checkJournalDesired(t *testing.T, ctx context.Context, j ports.Journal) {
	want := ledger.DesiredState{Rev: 7, FleetMode: ledger.FleetDrain, Leases: []ledger.DesiredLease{{WorkOrder: "a", ClaimID: "c", Fence: 1, State: "claimed"}}}
	if err := j.SaveDesired(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := j.Desired(ctx)
	if err != nil || got.Rev != 7 || got.FleetMode != ledger.FleetDrain || len(got.Leases) != 1 {
		t.Errorf("desired %+v: %v", got, err)
	}
}

func checkJournalMigrate(t *testing.T, ctx context.Context, j ports.Journal) {
	if _, err := j.Append(ctx, ledger.UploadEvent{Type: "note", OccurredAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := j.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := j.Pending(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Errorf("%d pending: %v", len(pending), err)
	}
}
