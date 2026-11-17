package contract

import (
	"context"
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

func digestEvent(generation int64, payload string) ledger.UploadEvent {
	return ledger.UploadEvent{
		Type: ledger.UploadGenerationDigest, Lane: "lane", WorkOrder: "wo", ClaimID: "c", Fence: 1, Generation: generation,
		Payload: []byte(payload), OccurredAt: 1,
	}
}

func pendingTypes(t *testing.T, ctx context.Context, j ports.Journal) []string {
	t.Helper()
	pending, err := j.Pending(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, event := range pending {
		types = append(types, event.Type)
	}
	return types
}

// checkDigestSaves holds what a worker keeps of a generation's digest while it runs: the state, replaced by each
// save, until the generation is over and the digest is final.
func checkDigestSaves(t *testing.T, ctx context.Context, j ports.Journal) {
	if _, err := j.Digest(ctx, "lane", 1); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("a digest nobody saved: %v", err)
	}
	for _, state := range []string{`{"n":1}`, `{"n":2}`} {
		if err := j.SaveDigest(ctx, "lane", 1, []byte(state), false); err != nil {
			t.Fatal(err)
		}
	}
	got, err := j.Digest(ctx, "lane", 1)
	if err != nil || string(got.State) != `{"n":2}` || got.Over || got.Recorded || got.Lane != "lane" || got.Generation != 1 {
		t.Fatalf("the digest after two saves: %+v, %v", got, err)
	}
	if err := j.SaveDigest(ctx, "lane", 1, []byte(`{"n":3}`), true); err != nil {
		t.Fatal(err)
	}
	if got, err = j.Digest(ctx, "lane", 1); err != nil || string(got.State) != `{"n":3}` || !got.Over {
		t.Errorf("the digest at its end: %+v, %v", got, err)
	}
	if other, err := j.Digest(ctx, "lane", 2); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("another generation's digest: %+v, %v", other, err)
	}
}

// checkDigestRecordsOnce is FR-SLOG-2 in the journal: the event that carries a digest and the mark that it was
// recorded are one write, and a repeat stores nothing.
func checkDigestRecordsOnce(t *testing.T, ctx context.Context, j ports.Journal) {
	if first, err := j.RecordDigest(ctx, "lane", 1, digestEvent(1, `{}`)); err != nil || first {
		t.Fatalf("a digest nobody saved recorded: %v, %v", first, err)
	}
	if err := j.SaveDigest(ctx, "lane", 1, []byte(`{"n":1}`), false); err != nil {
		t.Fatal(err)
	}
	if first, err := j.RecordDigest(ctx, "lane", 1, digestEvent(1, `{}`)); err != nil || first {
		t.Fatalf("a generation still running recorded: %v, %v", first, err)
	}
	if err := j.SaveDigest(ctx, "lane", 1, []byte(`{"n":2}`), true); err != nil {
		t.Fatal(err)
	}
	first, err := j.RecordDigest(ctx, "lane", 1, digestEvent(1, `{"source":"stream"}`))
	if err != nil || !first {
		t.Fatalf("the first record: %v, %v", first, err)
	}
	for range 2 {
		if again, err := j.RecordDigest(ctx, "lane", 1, digestEvent(1, `{"source":"stream"}`)); err != nil || again {
			t.Errorf("a repeat recorded: %v, %v", again, err)
		}
	}
	pending, err := j.Pending(ctx, 100)
	if err != nil || len(pending) != 1 || pending[0].Type != ledger.UploadGenerationDigest || pending[0].Generation != 1 || string(pending[0].Payload) != `{"source":"stream"}` {
		t.Errorf("pending %+v, %v", pending, err)
	}
	if err := j.SaveDigest(ctx, "lane", 1, []byte(`{"n":9}`), true); err != nil {
		t.Fatal(err)
	}
	if got, err := j.Digest(ctx, "lane", 1); err != nil || string(got.State) != `{"n":2}` || !got.Recorded {
		t.Errorf("a save after the record changed it: %+v, %v", got, err)
	}
}

// checkUnrecordedDigests is the restart: a generation that is over and has no event is the one a crash between
// its end and its record left, and it is the only one listed.
func checkUnrecordedDigests(t *testing.T, ctx context.Context, j ports.Journal) {
	if err := j.SaveDigest(ctx, "lane", 1, []byte(`{"n":1}`), true); err != nil {
		t.Fatal(err)
	}
	if err := j.SaveDigest(ctx, "lane", 2, []byte(`{"n":2}`), true); err != nil {
		t.Fatal(err)
	}
	if err := j.SaveDigest(ctx, "lane", 3, []byte(`{"n":3}`), false); err != nil {
		t.Fatal(err)
	}
	if _, err := j.RecordDigest(ctx, "lane", 1, digestEvent(1, `{}`)); err != nil {
		t.Fatal(err)
	}
	left, err := j.UnrecordedDigests(ctx)
	if err != nil || len(left) != 1 || left[0].Generation != 2 || string(left[0].State) != `{"n":2}` {
		t.Errorf("unrecorded %+v, %v", left, err)
	}
}

func checkForgottenLanesLoseTheirDigests(t *testing.T, ctx context.Context, j ports.Journal) {
	journalLane(t, ctx, j)
	if err := j.SaveDigest(ctx, "lane", 1, []byte(`{}`), true); err != nil {
		t.Fatal(err)
	}
	if _, err := j.ForgetLanes(ctx, []string{"lane"}, ledger.UploadEvent{Type: ledger.UploadLaneArchived, Lane: "lane", OccurredAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Digest(ctx, "lane", 1); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a forgotten lane's digest: %v", err)
	}
	if left, err := j.UnrecordedDigests(ctx); err != nil || len(left) != 0 {
		t.Errorf("unrecorded %+v, %v", left, err)
	}
	if got := pendingTypes(t, ctx, j); len(got) != 1 || got[0] != ledger.UploadLaneArchived {
		t.Errorf("pending %v", got)
	}
}
