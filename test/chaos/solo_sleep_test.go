//go:build chaos

package chaos

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
)

// These tests cover how the solo-sleep check judges a round, and start no daemon.

// soloWake is the Unix millisecond of a round's wake in these tests.
const soloWake = 1_000_000

func soloLapse(rev, fromWake int64) ledger.Event {
	return ledger.Event{Rev: rev, Type: workorders.EventType(workorders.TriggerLeaseExpired), RecordedAt: soloWake + fromWake}
}

func soloReturn(rev, fromWake int64) ledger.Event {
	return ledger.Event{Rev: rev, Type: workorders.EventType(workorders.TriggerReturnedUnstarted), RecordedAt: soloWake + fromWake}
}

func TestSoloRoundStopMustLeadTheFirstReadLeaseByTheMargin(t *testing.T) {
	const leaseUntil = 50_000
	margin := soloSleepMargin.Milliseconds()
	if reason := soloSleepStopReason(leaseUntil-margin-1, leaseUntil); reason != "" {
		t.Errorf("a stop %dms before the lease ends is inconclusive: %s", margin+1, reason)
	}
	for _, stopped := range []int64{leaseUntil - margin, leaseUntil, leaseUntil + 300} {
		if reason := soloSleepStopReason(stopped, leaseUntil); reason == "" {
			t.Errorf("a stop at %d under a lease until %d is conclusive", stopped, leaseUntil)
		}
	}
}

func TestSoloRoundJudgesTheLapsesAndReturns(t *testing.T) {
	window := soloSleepWindow()
	for _, c := range []struct {
		name         string
		events       []ledger.Event
		inconclusive bool
		violation    bool
	}{
		{name: "a lapse after the wake and its return", events: []ledger.Event{
			{Rev: 4, Type: reportedEvent, RecordedAt: soloWake - 3000}, soloLapse(5, 1), soloReturn(6, 30),
		}},
		{name: "a late beat adds a lapse and its return", events: []ledger.Event{
			soloLapse(5, 1), soloReturn(6, 30), soloLapse(7, 4000), soloReturn(8, 4030),
		}},
		{name: "the kill lands between a late lapse and its return", events: []ledger.Event{
			soloLapse(5, 1), soloReturn(6, 30), soloLapse(7, 4000),
		}},
		{name: "the first lapse at the wake", events: []ledger.Event{soloLapse(5, 0), soloReturn(6, 30)}},
		{name: "the first lapse just inside the window", events: []ledger.Event{soloLapse(5, window-1), soloReturn(6, window+20)}},
		{name: "the first lapse at the end of the window", events: []ledger.Event{
			soloLapse(5, window), soloReturn(6, window+20),
		}, inconclusive: true},
		{name: "the first lapse after the lease could be revived and expire again", events: []ledger.Event{
			soloLapse(5, soloSleepTTL.Milliseconds()+1), soloReturn(6, soloSleepTTL.Milliseconds()+30),
		}, inconclusive: true},
		{name: "the first lapse at the await bound", events: []ledger.Event{
			soloLapse(5, soloSleepAwait.Milliseconds()), soloReturn(6, soloSleepAwait.Milliseconds()+20),
		}, inconclusive: true},
		{name: "the first lapse before the wake", events: []ledger.Event{
			soloLapse(3, -2500), soloReturn(4, -2470), soloLapse(5, 1), soloReturn(6, 30),
		}, inconclusive: true},
		{name: "a late first lapse with no return", events: []ledger.Event{soloLapse(5, window+5)}, inconclusive: true},
		{name: "no lapse and no return", violation: true},
		{name: "a lapse with no return", events: []ledger.Event{soloLapse(5, 1)}, violation: true},
		{name: "a second lapse with no return", events: []ledger.Event{soloLapse(5, 1), soloLapse(6, 2)}, violation: true},
		{name: "a return with no lapse", events: []ledger.Event{soloReturn(5, 1)}, violation: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			reason, err := soloSleepLapses(c.events, soloWake, t.Logf)
			var violation *contract.Violation
			switch {
			case c.violation:
				if !errors.As(err, &violation) || reason != "" {
					t.Errorf("got %q and %v, want a violation", reason, err)
				}
			case err != nil:
				t.Errorf("got %v", err)
			case c.inconclusive && reason == "":
				t.Error("the round is conclusive")
			case !c.inconclusive && reason != "":
				t.Errorf("the round is inconclusive: %s", reason)
			}
		})
	}
}

// soloOutcome is what one round returns.
type soloOutcome struct {
	reason string
	err    error
}

func TestSoloRoundRetriesOnlyAnInconclusiveRound(t *testing.T) {
	late := soloOutcome{reason: "stopped late"}
	caught := soloOutcome{err: contract.Violatedf("no return")}
	harness := soloOutcome{err: errors.New("the daemon did not start")}
	for _, c := range []struct {
		name     string
		outcomes []soloOutcome
		rounds   int
		want     error
	}{
		{name: "a conclusive pass after inconclusive rounds", outcomes: []soloOutcome{late, late, {}}, rounds: 3},
		{name: "a violation after an inconclusive round", outcomes: []soloOutcome{late, caught}, rounds: 2, want: caught.err},
		{name: "a harness error ends the retries", outcomes: []soloOutcome{harness}, rounds: 1, want: harness.err},
	} {
		t.Run(c.name, func(t *testing.T) {
			var attempts []int
			err := retrySoloSleep(soloSleepAttempts, t.Logf, func(attempt int) (string, error) {
				attempts = append(attempts, attempt)
				o := c.outcomes[len(attempts)-1]
				return o.reason, o.err
			})
			if !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
			if len(attempts) != c.rounds {
				t.Errorf("ran rounds %v, want %d", attempts, c.rounds)
			}
		})
	}
}

func TestSoloRoundExhaustedIsAFailureAndNeverACatch(t *testing.T) {
	var attempts []int
	err := retrySoloSleep(soloSleepAttempts, t.Logf, func(attempt int) (string, error) {
		attempts = append(attempts, attempt)
		return fmt.Sprintf("stopped late in round %d", attempt), nil
	})
	if len(attempts) != soloSleepAttempts || attempts[0] != 1 || attempts[len(attempts)-1] != soloSleepAttempts {
		t.Errorf("ran rounds %v, want 1 to %d", attempts, soloSleepAttempts)
	}
	if !errors.Is(err, errSoloSleepInconclusive) {
		t.Fatalf("got %v, want %v", err, errSoloSleepInconclusive)
	}
	var violation *contract.Violation
	if errors.As(err, &violation) {
		t.Errorf("an inconclusive check counts as a catch: %v", err)
	}
	for _, attempt := range attempts {
		if want := fmt.Sprintf("stopped late in round %d", attempt); !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not name %q", err, want)
		}
	}
}
