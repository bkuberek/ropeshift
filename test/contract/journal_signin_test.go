package contract

import (
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

func TestJournalStartsWithNothingOfASignIn(t *testing.T) {
	j := openTestJournal(t)
	got, err := j.SignIn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, lanes.SignInState{}) {
		t.Errorf("a new journal holds %+v", got)
	}
}

// TestJournalKeepsASignInWithWhatFollows holds a state and the word of it to one write, which is
// the crash window a separate append would leave.
func TestJournalKeepsASignInWithWhatFollows(t *testing.T) {
	j := openTestJournal(t)
	ctx := t.Context()
	want := lanes.SignInState{
		Last:    lanes.UsageReading{Status: lanes.UsageRejected, ResetsAtMs: 5, Window: "five_hour", UsedPercent: 91, UsedKnown: true},
		Limited: true, ResetAtMs: 5, Probe: "lane-1", ProbeAtMs: 9, Tries: 2, Hint: "Current session", HintRead: true, SignedOut: true,
	}
	if err := j.SaveSignIn(ctx, want, eventOf(ledger.UploadWakeSignal)); err != nil {
		t.Fatal(err)
	}
	got, err := j.SignIn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if count := pendingCount(t, j); count != 1 {
		t.Errorf("%d events", count)
	}
	next := lanes.SignInState{Last: want.Last}
	if err := j.SaveSignIn(ctx, next); err != nil {
		t.Fatal(err)
	}
	got, err = j.SignIn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, next) {
		t.Errorf("a second save reads %+v", got)
	}
	if count := pendingCount(t, j); count != 1 {
		t.Errorf("a save with no word stored %d events", count)
	}
}
