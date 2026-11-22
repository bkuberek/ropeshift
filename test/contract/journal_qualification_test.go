package contract

import (
	"reflect"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

func TestJournalStartsWithNoQualification(t *testing.T) {
	t.Parallel()
	j := openTestJournal(t)
	got, err := j.Qualification(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "" || got.Holds("2.1.316") {
		t.Errorf("a new journal holds %+v", got)
	}
}

// TestJournalKeepsAQualificationWithItsEvent holds a probe's result and the event that records it
// to one write, so a worker never runs on a result the ledger will not hear of, and a later probe
// replaces it.
func TestJournalKeepsAQualificationWithItsEvent(t *testing.T) {
	t.Parallel()
	j := openTestJournal(t)
	ctx := t.Context()
	want := lanes.Qualification{
		Driver: "claude-code", Version: "2.1.317", Source: lanes.QualifiedByProbe, SignIn: "subscription",
		Checks: map[lanes.Surface]lanes.CheckResult{"socket_line": lanes.CheckPassed, "receipts": lanes.CheckUnobserved}, Degraded: true,
	}
	if err := j.SaveQualification(ctx, want, eventOf(ledger.UploadDriverQualified)); err != nil {
		t.Fatal(err)
	}
	got, err := j.Qualification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if count := pendingCount(t, j); count != 1 {
		t.Errorf("%d events", count)
	}
	next := want
	next.Version, next.Degraded = "2.1.318", false
	if err := j.SaveQualification(ctx, next, eventOf(ledger.UploadDriverQualified)); err != nil {
		t.Fatal(err)
	}
	if got, err = j.Qualification(ctx); err != nil || got.Version != "2.1.318" {
		t.Errorf("a later probe reads %+v, %v", got, err)
	}
	if count := pendingCount(t, j); count != 2 {
		t.Errorf("%d events after two probes", count)
	}
}

// TestJournalKeepsWhenTheLastProbeBegan is what a restarted worker reads to hold back its probe: none before the first
// attempt, and then the last one, which each later attempt replaces.
func TestJournalKeepsWhenTheLastProbeBegan(t *testing.T) {
	t.Parallel()
	j := openTestJournal(t)
	ctx := t.Context()
	got, err := j.ProbeAttempt(ctx)
	if err != nil || got.Version != "" || !got.BeganAt.IsZero() {
		t.Fatalf("a new journal holds the attempt %+v, %v", got, err)
	}
	first := lanes.ProbeAttempt{Version: "2.1.317", BeganAt: time.UnixMilli(1_700_000_000_123)}
	if err := j.SaveProbeAttempt(ctx, first); err != nil {
		t.Fatal(err)
	}
	if got, err = j.ProbeAttempt(ctx); err != nil || got.Version != first.Version || !got.BeganAt.Equal(first.BeganAt) {
		t.Fatalf("the journal reads %+v, %v, want %+v", got, err, first)
	}
	next := lanes.ProbeAttempt{Version: "2.1.318", BeganAt: first.BeganAt.Add(time.Hour)}
	if err := j.SaveProbeAttempt(ctx, next); err != nil {
		t.Fatal(err)
	}
	if got, err = j.ProbeAttempt(ctx); err != nil || got.Version != next.Version || !got.BeganAt.Equal(next.BeganAt) {
		t.Errorf("a later attempt reads %+v, %v, want %+v", got, err, next)
	}
}
