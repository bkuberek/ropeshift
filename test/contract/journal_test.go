package contract

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/lanes"
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
		{"an effect key is recorded once", checkEffectOnce},
		{"a crash leaves a started effect unknown", checkEffectUnknown},
		{"only an unknown effect can be retried", checkEffectRetry},
		{"a settled effect stays settled", checkEffectSettled},
		{"a generation binds once", checkGenerationBinds},
		{"progress round trips and survives a save", checkLaneProgress},
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

func newEffect(label string) lanes.Effect {
	return lanes.NewEffect(lanes.EffectSession, "lane", 1, "the-target", label)
}

func effectState(t *testing.T, ctx context.Context, j ports.Journal, key string) lanes.EffectState {
	t.Helper()
	effect, err := j.Effect(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	return effect.State
}

func checkEffectOnce(t *testing.T, ctx context.Context, j ports.Journal) {
	effect := newEffect("")
	first, err := j.BeginEffect(ctx, effect)
	if err != nil || !first {
		t.Fatalf("first begin: %v, %v", first, err)
	}
	again, err := j.BeginEffect(ctx, effect)
	if err != nil || again {
		t.Fatalf("second begin: %v, %v", again, err)
	}
	all, err := j.Effects(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("effects %+v: %v", all, err)
	}
	if got := all[0]; got.Key != effect.Key || got.Target != "the-target" || got.State != lanes.EffectStarted || got.Attempts != 1 {
		t.Errorf("effect %+v", got)
	}
	if _, err := j.Effect(ctx, "no such key"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("missing effect: %v", err)
	}
}

func checkEffectUnknown(t *testing.T, ctx context.Context, j ports.Journal) {
	started, finished := newEffect("started"), newEffect("finished")
	for _, effect := range []lanes.Effect{started, finished} {
		if _, err := j.BeginEffect(ctx, effect); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.FinishEffect(ctx, finished.Key, lanes.EffectCompleted, ""); err != nil {
		t.Fatal(err)
	}
	marked, err := j.MarkUnknown(ctx)
	if err != nil || marked != 1 {
		t.Fatalf("marked %d: %v", marked, err)
	}
	if got := effectState(t, ctx, j, started.Key); got != lanes.EffectUnknown {
		t.Errorf("started effect is %s", got)
	}
	if got := effectState(t, ctx, j, finished.Key); got != lanes.EffectCompleted {
		t.Errorf("finished effect is %s", got)
	}
}

func checkEffectRetry(t *testing.T, ctx context.Context, j ports.Journal) {
	effect := newEffect("")
	if _, err := j.BeginEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	if moved, err := j.RetryEffect(ctx, effect.Key); err != nil || moved {
		t.Fatalf("a started effect moved: %v, %v", moved, err)
	}
	if _, err := j.MarkUnknown(ctx); err != nil {
		t.Fatal(err)
	}
	if moved, err := j.RetryEffect(ctx, effect.Key); err != nil || !moved {
		t.Fatalf("an unknown effect stayed: %v, %v", moved, err)
	}
	got, err := j.Effect(ctx, effect.Key)
	if err != nil || got.State != lanes.EffectStarted || got.Attempts != 2 {
		t.Errorf("effect %+v: %v", got, err)
	}
}

func checkEffectSettled(t *testing.T, ctx context.Context, j ports.Journal) {
	effect := newEffect("")
	if _, err := j.BeginEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	if err := j.FinishEffect(ctx, effect.Key, lanes.EffectCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if err := j.FinishEffect(ctx, effect.Key, lanes.EffectFailed, "too late"); err != nil {
		t.Fatal(err)
	}
	if got := effectState(t, ctx, j, effect.Key); got != lanes.EffectCompleted {
		t.Errorf("a settled effect moved to %s", got)
	}
}

func journalLane(t *testing.T, ctx context.Context, j ports.Journal) lanes.Record {
	t.Helper()
	record := lanes.Record{ID: "lane", WorkOrder: "wo", ClaimID: "c", Fence: 1, Generation: 1, State: lanes.StateRunning}
	if err := j.SaveLane(ctx, record); err != nil {
		t.Fatal(err)
	}
	return record
}

func checkGenerationBinds(t *testing.T, ctx context.Context, j ports.Journal) {
	journalLane(t, ctx, j)
	event := ledger.UploadEvent{Epoch: 1, Type: ledger.UploadGenerationBound, Lane: "lane", OccurredAt: 1}
	first, err := j.BindGeneration(ctx, "lane", "session-1", event)
	if err != nil || !first {
		t.Fatalf("first bind: %v, %v", first, err)
	}
	again, err := j.BindGeneration(ctx, "lane", "session-2", event)
	if err != nil || again {
		t.Fatalf("second bind: %v, %v", again, err)
	}
	pending, err := j.Pending(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("%d events: %v", len(pending), err)
	}
	record, err := j.Lane(ctx, "lane")
	if err != nil || record.SessionID != "session-1" {
		t.Errorf("lane %+v: %v", record, err)
	}
}

func checkLaneProgress(t *testing.T, ctx context.Context, j ports.Journal) {
	record := journalLane(t, ctx, j)
	want := lanes.Progress{Cursor: 12, Cost: lanes.CostMeter{SessionTotalUSD: 0.5, CostUSD: 0.25, UnknownTurns: 1}, Usage: lanes.Usage{OutputTokens: 9}}
	if err := j.SaveProgress(ctx, "lane", want); err != nil {
		t.Fatal(err)
	}
	if err := j.SaveLane(ctx, record); err != nil {
		t.Fatal(err)
	}
	got, err := j.Lane(ctx, "lane")
	if err != nil || got.Progress != want {
		t.Errorf("progress %+v: %v", got.Progress, err)
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

func TestJournalChargesARestartOnce(t *testing.T) {
	journal := openTestJournal(t)
	ctx := t.Context()
	charge := lanes.Restart{Key: "g2", AtMs: 1, NotBeforeMs: 31}
	first, err := journal.ChargeRestart(ctx, "lane-1", charge)
	if err != nil || !first {
		t.Fatalf("first charge: %v, %v", first, err)
	}
	again, err := journal.ChargeRestart(ctx, "lane-1", lanes.Restart{Key: "g2", AtMs: 99, NotBeforeMs: 99})
	if err != nil || again {
		t.Fatalf("second charge: %v, %v", again, err)
	}
	charges, err := journal.Restarts(ctx, "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(charges) != 1 || charges[0] != charge {
		t.Errorf("charges %+v", charges)
	}
}

func TestJournalClearsChargesAndCountsTheReset(t *testing.T) {
	journal := openTestJournal(t)
	ctx := t.Context()
	record := lanes.Record{ID: "lane-1", WorkOrder: "wo", ClaimID: "c", Fence: 1, Generation: 1, State: lanes.StateRunning}
	if err := journal.SaveLane(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.ChargeRestart(ctx, "lane-1", lanes.Restart{Key: "g2", AtMs: 1}); err != nil {
		t.Fatal(err)
	}
	if err := journal.ClearRestarts(ctx, "lane-1", 3); err != nil {
		t.Fatal(err)
	}
	charges, err := journal.Restarts(ctx, "lane-1")
	if err != nil || len(charges) != 0 {
		t.Errorf("charges %+v, %v", charges, err)
	}
	got, err := journal.Lane(ctx, "lane-1")
	if err != nil || got.ResetsSeen != 3 {
		t.Errorf("record %+v, %v", got, err)
	}
}

func TestJournalBeginsAGenerationWithItsEvent(t *testing.T) {
	journal := openTestJournal(t)
	ctx := t.Context()
	record := lanes.Record{ID: "lane-1", WorkOrder: "wo", ClaimID: "c", Fence: 1, Generation: 1, State: lanes.StateRunning, TokenDigest: "d"}
	if err := journal.SaveLane(ctx, record); err != nil {
		t.Fatal(err)
	}
	record.Generation, record.ResumeSession = 2, "session-1"
	event := ledger.UploadEvent{Type: ledger.UploadGenerationStarted, Lane: "lane-1", Generation: 2, WorkOrder: "wo", ClaimID: "c", Fence: 1, OccurredAt: 1}
	if _, err := journal.BeginGeneration(ctx, record, event); err != nil {
		t.Fatal(err)
	}
	got, err := journal.Lane(ctx, "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != 2 || got.ResumeSession != "session-1" || got.SessionID != "" || got.TokenDigest != "" {
		t.Errorf("record %+v", got)
	}
	pending, err := journal.Pending(ctx, 10)
	if err != nil || len(pending) != 1 || pending[0].Type != ledger.UploadGenerationStarted {
		t.Errorf("pending %+v, %v", pending, err)
	}
}

func TestJournalKeepsAHandoffThroughItsSteps(t *testing.T) {
	journal := openTestJournal(t)
	ctx := t.Context()
	record := lanes.Record{ID: "lane-1", WorkOrder: "wo", ClaimID: "c", Fence: 1, Generation: 1, State: lanes.StateRunning, TokenDigest: "d"}
	if err := journal.SaveLane(ctx, record); err != nil {
		t.Fatal(err)
	}
	progress := lanes.HandoffProgress{Reason: "r", DeadlineMs: 5, Narrative: &lanes.Narrative{NextSteps: []string{"go on"}, CarriesNextStep: true}}
	if err := journal.SaveHandoff(ctx, "lane-1", &progress); err != nil {
		t.Fatal(err)
	}
	got, err := journal.Lane(ctx, "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Handoff == nil || got.Handoff.DeadlineMs != 5 || !got.Handoff.Narrative.CarriesNextStep || !got.Handoff.Open() {
		t.Fatalf("handoff %+v", got.Handoff)
	}
	progress.Document, progress.Digest = []byte(`{"schema":"x"}`), "sha256:x"
	event := ledger.UploadEvent{Type: ledger.UploadHandoffStored, Lane: "lane-1", Generation: 1, WorkOrder: "wo", ClaimID: "c", Fence: 1, OccurredAt: 1}
	if _, err := journal.StoreHandoff(ctx, "lane-1", progress, event); err != nil {
		t.Fatal(err)
	}
	pending, err := journal.Pending(ctx, 10)
	if err != nil || len(pending) != 1 || pending[0].Type != ledger.UploadHandoffStored {
		t.Errorf("pending %+v, %v", pending, err)
	}
	got, err = journal.Lane(ctx, "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Handoff == nil || got.Handoff.Digest != "sha256:x" || !got.Handoff.Open() {
		t.Fatalf("a stored handoff %+v", got.Handoff)
	}
	got.Generation, got.Handoff.Begun = 2, true
	if _, err := journal.BeginGeneration(ctx, got, event); err != nil {
		t.Fatal(err)
	}
	after, err := journal.Lane(ctx, "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Handoff == nil || after.Handoff.Open() || string(after.Handoff.Document) != `{"schema":"x"}` {
		t.Errorf("a begun handoff %+v", after.Handoff)
	}
	if err := journal.SaveHandoff(ctx, "lane-1", nil); err != nil {
		t.Fatal(err)
	}
	if cleared, err := journal.Lane(ctx, "lane-1"); err != nil || cleared.Handoff != nil {
		t.Errorf("a cleared handoff %+v, %v", cleared.Handoff, err)
	}
}

func TestJournalKeepsWhichChargesWerePlanned(t *testing.T) {
	journal := openTestJournal(t)
	ctx := t.Context()
	record := lanes.Record{ID: "lane-1", WorkOrder: "wo", ClaimID: "c", Fence: 1, Generation: 1, State: lanes.StateRunning}
	if err := journal.SaveLane(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.ChargeRestart(ctx, "lane-1", lanes.Restart{Key: "g2", AtMs: 1, Planned: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.ChargeRestart(ctx, "lane-1", lanes.Restart{Key: "g3", AtMs: 2}); err != nil {
		t.Fatal(err)
	}
	charges, err := journal.Restarts(ctx, "lane-1")
	if err != nil || len(charges) != 2 || !charges[0].Planned || charges[1].Planned {
		t.Errorf("charges %+v, %v", charges, err)
	}
}

func openTestJournal(t *testing.T) ports.Journal {
	t.Helper()
	journal, err := fake.NewJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return journal
}

func openJournal(t *testing.T) ports.Journal {
	t.Helper()
	journal, err := sqlite.OpenJournal(filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return journal
}

func TestJournalAllocations(t *testing.T) {
	journal := openJournal(t)
	ctx := t.Context()
	pool := lanes.PortPool{Base: 20000, Size: 20}
	first, err := journal.Allocate(ctx, lanes.AllocationRequest{Lane: "one", Ports: 10, ComposeProject: "rs-one", Pool: pool})
	if err != nil || first.PortBase != 20000 || first.ComposeProject != "rs-one" {
		t.Fatalf("first %+v: %v", first, err)
	}
	second, err := journal.Allocate(ctx, lanes.AllocationRequest{Lane: "two", Ports: 10, ComposeProject: "rs-two", Pool: pool})
	if err != nil || second.PortBase != 20010 {
		t.Fatalf("second %+v: %v", second, err)
	}
	again, err := journal.Allocate(ctx, lanes.AllocationRequest{Lane: "one", Ports: 10, ComposeProject: "rs-one", Pool: pool})
	if err != nil || again != first {
		t.Errorf("a retry got %+v: %v", again, err)
	}
	if _, err := journal.Allocate(ctx, lanes.AllocationRequest{Lane: "three", Ports: 10, Pool: pool}); !errors.Is(err, ports.ErrNoPorts) {
		t.Errorf("a full pool: %v", err)
	}
	if err := journal.Release(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if err := journal.Release(ctx, "one"); err != nil {
		t.Errorf("a repeat: %v", err)
	}
	third, err := journal.Allocate(ctx, lanes.AllocationRequest{Lane: "three", Ports: 10, Pool: pool})
	if err != nil || third.PortBase != 20000 {
		t.Errorf("a released block: %+v: %v", third, err)
	}
	held, err := journal.Allocations(ctx)
	if err != nil || len(held) != 2 {
		t.Errorf("held %+v: %v", held, err)
	}
}

func TestJournalAllocationsRaceForOneBlock(t *testing.T) {
	journal := openJournal(t)
	ctx := t.Context()
	pool := lanes.PortPool{Base: 20000, Size: 1000}
	const racers = 16
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			if _, err := journal.Allocate(ctx, lanes.AllocationRequest{Lane: fmt.Sprint("lane-", i), Ports: 10, Pool: pool}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	held, err := journal.Allocations(ctx)
	if err != nil || len(held) != racers {
		t.Fatalf("held %+v: %v", held, err)
	}
	seen := map[int64]bool{}
	for _, allocation := range held {
		if seen[allocation.PortBase] {
			t.Errorf("two lanes share %d", allocation.PortBase)
		}
		seen[allocation.PortBase] = true
	}
}

func TestJournalKeepsAGuard(t *testing.T) {
	journal := openJournal(t)
	ctx := t.Context()
	if err := journal.SaveLane(ctx, lanes.Record{ID: "lane", WorkOrder: "w", ClaimID: "c", State: lanes.StateRunning}); err != nil {
		t.Fatal(err)
	}
	want := lanes.GuardResult{Unpushed: true, Ref: "refs/ropeshift/salvage/lane/f1", Commit: "abc", Detail: "pushing"}
	if err := journal.SaveGuard(ctx, "lane", want); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.MarkArchived(ctx, "lane", ledger.UploadEvent{Type: ledger.UploadLaneArchived, OccurredAt: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := journal.Lane(ctx, "lane")
	if err != nil || got.Guard != want || got.State != lanes.StateArchived {
		t.Errorf("lane %+v: %v", got, err)
	}
}
