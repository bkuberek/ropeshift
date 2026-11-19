package worker_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// ciRig is a pull request the worker watches, whose required check began at the worker's clock.
func ciRig(t *testing.T) (*forgeRig, func() int, time.Time) {
	t.Helper()
	f := newForgeRig(t, func(forge *fake.Forge, _ *fake.Worktrees) { forge.Push(widgets, "lane/lane-1", fake.LaneHead) })
	for len(f.forge.PullRequests()) == 0 {
		f.converge(1)
	}
	f.clockAtDatabaseTime()
	started := f.clock.Now()
	f.forge.SetChecks(f.forge.PullRequests()[0].Number, ledger.PullRequestCheck{
		Name: "CI", State: ledger.CheckPending, Status: ledger.CIRunInProgress, StartedAt: started.UTC().Format(time.RFC3339), Required: true,
	})
	reads := func() int { return forgeReads(f.forge.Calls()) }
	return f, reads, started
}

// forgeReads counts the reads of the forge a schedule made: a read of one pull request, a query of several, and a
// probe that found nothing changed. A probe that found a change is the first half of the read that follows it.
func forgeReads(calls []string) int {
	// A sign-in check runs beside a read and may land between its two halves.
	calls = slices.DeleteFunc(slices.Clone(calls), func(call string) bool { return strings.HasPrefix(call, "health ") })
	count := 0
	for i, call := range calls {
		switch {
		case strings.HasPrefix(call, "read "), strings.HasPrefix(call, "batch "):
			count++
		case strings.HasPrefix(call, "probe "):
			if i+1 >= len(calls) || calls[i+1] != "read "+strings.TrimPrefix(call, "probe ") {
				count++
			}
		}
	}
	return count
}

// advance moves the worker's clock and runs a pass, as the daemon's loop does.
func (f *forgeRig) advance(d time.Duration) {
	f.t.Helper()
	f.clock.Advance(d)
	f.converge(1)
}

// FR-SPEND-20: a watched pull request is read on the repository's CI schedule and no longer every minute. The first
// read is at once, the next at the p50 of the no-history default, 10 minutes, and the ones after it close in on the
// p85 by halves.
func TestAWatchedPullRequestIsReadOnTheCISchedule(t *testing.T) {
	t.Parallel()
	f, reads, _ := ciRig(t)
	f.converge(2)
	first := reads()
	if first == 0 {
		t.Fatalf("the worker never read the pull request: %v", f.forge.Calls())
	}
	f.advance(5 * time.Minute)
	f.advance(4 * time.Minute)
	if got := reads(); got != first {
		t.Fatalf("a run 9 minutes in was read again before its p50: %d reads, %d before", got, first)
	}
	f.advance(2 * time.Minute)
	if got := reads(); got != first+1 {
		t.Fatalf("a run past its p50 was not read once: %d reads, %d before", got, first)
	}
	f.advance(time.Minute)
	if got := reads(); got != first+1 {
		t.Fatalf("a read a minute after the p50 read: %d reads", got)
	}
	f.advance(5 * time.Minute)
	if got := reads(); got != first+2 {
		t.Fatalf("the read half way to the p85 did not come: %d reads", got)
	}
}

// FR-SPEND-21: once the repository has history, the schedule reads by the repository's own p50 and not by the
// no-history default. Five records of two minutes put the first read of a running check at two minutes.
func TestAWatchedPullRequestIsReadByTheRepositorysOwnHistory(t *testing.T) {
	t.Parallel()
	f := newForgeRig(t, func(forge *fake.Forge, _ *fake.Worktrees) { forge.Push(widgets, "lane/lane-1", fake.LaneHead) })
	err := f.store.Write(t.Context(), scope, "note", func(tx ports.WriteTx) error {
		for i := range 5 {
			done := int64(i+1) * (time.Hour).Milliseconds()
			record := ledger.CIRecord{
				Repository: "repo-1", Head: "h" + string(rune('a'+i)), Attempt: 1, PushedAt: done - 120_000, FirstStart: done - 120_000,
				LastCompleted: done, DurationMs: 120_000, Result: ledger.CIPass,
			}
			if _, err := tx.Forge().InsertCIRecord(record); err != nil {
				return err
			}
		}
		_, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: member, Subject: ledger.Subject{Kind: "note", ID: "ci"}, Data: []byte(`{}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for len(f.forge.PullRequests()) == 0 {
		f.converge(1)
	}
	f.clockAtDatabaseTime()
	f.forge.SetChecks(f.forge.PullRequests()[0].Number, ledger.PullRequestCheck{
		Name: "CI", State: ledger.CheckPending, Status: ledger.CIRunInProgress, StartedAt: f.clock.Now().UTC().Format(time.RFC3339), Required: true,
	})
	reads := func() int {
		count := 0
		for _, call := range f.forge.Calls() {
			if strings.HasPrefix(call, "read ") {
				count++
			}
		}
		return count
	}
	f.converge(2)
	first := reads()
	f.advance(time.Minute)
	if got := reads(); got != first {
		t.Fatalf("a run a minute in was read again before its own p50: %d reads, %d before", got, first)
	}
	f.advance(90 * time.Second)
	if got := reads(); got != first+1 {
		t.Fatalf("a run past its own p50 of two minutes was not read: %d reads, %d before", got, first)
	}
}

// A signed hint that a check changed reads the pull request at once, and a hint for another head or another
// repository does not.
func TestAHintReadsThePullRequestAtOnce(t *testing.T) {
	t.Parallel()
	f, reads, _ := ciRig(t)
	f.converge(2)
	first := reads()
	f.advance(time.Minute)
	if got := reads(); got != first {
		t.Fatalf("a read a minute in: %d reads", got)
	}
	f.subject.NoteCIHint(ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "other"}, "")
	f.advance(time.Second)
	if got := reads(); got != first {
		t.Fatalf("a hint for another repository read this one: %d reads", got)
	}
	f.subject.NoteCIHint(widgets, "")
	f.advance(time.Second)
	if got := reads(); got != first+1 {
		t.Fatalf("a hint did not read the pull request: %d reads, %d before", got, first)
	}
	f.advance(time.Second)
	if got := reads(); got != first+1 {
		t.Fatalf("a hint read twice: %d reads", got)
	}
}

// A CI run that takes three hours is read about every steady interval once past the expected time, starts no
// session, and leaves the lane's own session the only one.
func TestAThreeHourCIRunIsReadOnScheduleAndStartsNoSession(t *testing.T) {
	t.Parallel()
	f, reads, _ := ciRig(t)
	f.converge(2)
	sessions := len(f.driver.Specs())
	for range 360 {
		f.advance(30 * time.Second)
	}
	if got := reads(); got < 30 || got > 50 {
		t.Errorf("a 3 hour run took %d reads", got)
	}
	if got := len(f.driver.Specs()); got != sessions {
		t.Errorf("waiting on CI started %d sessions", got-sessions)
	}
}
