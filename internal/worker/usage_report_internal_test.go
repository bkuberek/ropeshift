package worker

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A report says what the worker knows of its sign-in's limit: at a limit, the reset it waits on, and otherwise the last
// any session named, with how long ago it heard, because only durations cross the wire.
func TestAReportSaysTheSignInsLimit(t *testing.T) {
	state := lanes.SignInState{
		Last:    lanes.UsageReading{Window: ledger.UsageWindowSession, ResetsAtMs: 9_000, UsedPercent: 100, UsedKnown: true, ReadAtMs: 4_000},
		Limited: true, ResetAtMs: 8_000,
	}
	got := signInUsage(state, 5_000)
	want := ledger.SignInUsage{Limited: true, Window: ledger.UsageWindowSession, ResetsAtMs: 8_000, Used: 100, UsedKnown: true, Source: ledger.UsageFromSession, ReadAgeMs: 1_000}
	if got == nil || *got != want {
		t.Errorf("the report says %+v, want %+v", got, want)
	}
	if passed := signInUsage(state, 8_000); passed.Limited || passed.ResetsAtMs != 9_000 {
		t.Errorf("a limit whose reset came reads %+v", passed)
	}
	quiet := signInUsage(lanes.SignInState{Limited: true}, 5_000)
	if quiet == nil || !quiet.Limited || quiet.ResetsAtMs != 0 || quiet.Source != "" || quiet.ReadAgeMs != 0 {
		t.Errorf("a limit nobody gave a reset for reads %+v", quiet)
	}
}
