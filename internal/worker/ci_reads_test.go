package worker_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// The cheap reads of CI (FR-SPEND-20): the worker reads the heads of a repository that are due in one query, asks
// a single due head with conditional requests, and holds its reads of a repository the forge is rate limiting.

func count(calls []string, prefix string) int {
	n := 0
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			n++
		}
	}
	return n
}

// watchOthers makes the worker watch two more pull requests of the repository, which the ledger already agrees
// with, so a read of them reports nothing, and returns their numbers.
func watchOthers(t *testing.T, f *forgeRig) []int64 {
	t.Helper()
	desired, err := f.journal.Desired(t.Context())
	if err != nil || len(desired.PullRequests) != 1 {
		t.Fatalf("the worker watches %+v: %v", desired.PullRequests, err)
	}
	template := desired.PullRequests[0]
	var numbers []int64
	for _, branch := range []string{"lane/other-1", "lane/other-2"} {
		f.forge.Push(widgets, branch, "head-"+branch)
		pr, err := f.forge.OpenPullRequest(t.Context(), ports.PullRequestSpec{Repo: ports.ForgeRepo{Binding: widgets}, Base: "main", Branch: branch})
		if err != nil {
			t.Fatal(err)
		}
		other := template
		other.WorkOrder, other.Number, other.HeadCommit, other.Mergeable, other.Checks = "wo-"+branch, pr.Number, pr.HeadCommit, pr.Mergeable, nil
		desired.PullRequests = append(desired.PullRequests, other)
		numbers = append(numbers, pr.Number)
	}
	if err := f.journal.SaveDesired(t.Context(), desired); err != nil {
		t.Fatal(err)
	}
	return numbers
}

// Two or more due heads of one repository are one query, and no read of a pull request on its own.
func TestDueHeadsOfOneRepositoryAreReadInOneQuery(t *testing.T) {
	t.Parallel()
	f, _, _ := ciRig(t)
	f.converge(2)
	numbers := watchOthers(t, f)
	before := len(f.forge.Calls())
	f.advance(time.Second)
	calls := slices.DeleteFunc(slices.Clone(f.forge.Calls()[before:]), func(call string) bool { return strings.HasPrefix(call, "health ") })
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "batch ") {
		t.Fatalf("two due heads took %v, want one query", calls)
	}
	for _, number := range numbers {
		if !strings.Contains(calls[0], strconv.FormatInt(number, 10)) {
			t.Errorf("the query %q leaves out pull request %d", calls[0], number)
		}
	}
}

// A head the query cannot answer is read on its own, in the same pass.
func TestAHeadTheQueryLeavesOutIsReadOnItsOwn(t *testing.T) {
	t.Parallel()
	f, _, _ := ciRig(t)
	f.converge(2)
	numbers := watchOthers(t, f)
	desired, err := f.journal.Desired(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// A pull request the forge does not hold is one the query cannot answer.
	desired.PullRequests[2].Number = 9999
	if err := f.journal.SaveDesired(t.Context(), desired); err != nil {
		t.Fatal(err)
	}
	before := len(f.forge.Calls())
	f.advance(time.Second)
	calls := f.forge.Calls()[before:]
	if count(calls, "batch ") != 1 || !slices.Contains(calls, "read 9999") || slices.Contains(calls, "read "+strconv.FormatInt(numbers[0], 10)) {
		t.Errorf("the pass took %v", calls)
	}
}

// One due head is probed, and a probe that finds nothing changed costs no read of the pull request. A change in a
// check, or a hint, makes the probe say so and the pull request is read.
func TestAnUnchangedHeadIsProbedAndNotRead(t *testing.T) {
	t.Parallel()
	f, _, _ := ciRig(t)
	f.converge(2)
	number := f.forge.PullRequests()[0].Number
	if count(f.forge.Calls(), "probe ") != 0 {
		t.Fatalf("the first read probed: %v", f.forge.Calls())
	}
	f.advance(11 * time.Minute)
	if got := f.forge.Calls(); count(got, "probe ") != 1 || count(got, "read ") != 2 {
		t.Fatalf("a run past its p50 took %v, want a probe with nothing to compare and a read", got)
	}
	reads := count(f.forge.Calls(), "read ")
	f.advance(5 * time.Minute)
	if got := f.forge.Calls(); count(got, "probe ") != 2 || count(got, "read ") != reads {
		t.Fatalf("the next due read of what did not change took %v, want a probe and no read", got)
	}
	f.forge.SetChecks(number, ledger.PullRequestCheck{Name: "CI", State: ledger.CheckPassed, Status: ledger.CIRunCompleted, Required: true})
	f.subject.NoteCIHint(widgets, "")
	f.advance(time.Second)
	if got := f.forge.Calls(); count(got, "probe ") != 3 || count(got, "read ") != reads+1 {
		t.Fatalf("a read after a check finished took %v, want a probe and a read", got)
	}
}

// A hint a heartbeat's answer carries reads the pull request it names at once, once, and the worker sends the mark
// of the last hint it heard so the next answer carries only what is new.
func TestAHintInAHeartbeatReadsThePullRequestAtOnce(t *testing.T) {
	t.Parallel()
	f, reads, _ := ciRig(t)
	f.converge(2)
	first := reads()
	board := control.NewCIHints(f.clock.Now)
	since := board.Since
	f.link.Hints.Store(&since)
	beat := func() {
		f.t.Helper()
		if _, err := f.subject.Beat(t.Context()); err != nil {
			t.Fatal(err)
		}
		f.advance(time.Second)
	}
	beat()
	if reads() != first {
		t.Fatalf("a heartbeat with no hint read the pull request: %d reads, %d before", reads(), first)
	}
	board.Note("project-1", ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "other"}, "")
	beat()
	if reads() != first {
		t.Fatalf("a hint for another repository read this one: %d reads", reads())
	}
	board.Note("project-1", widgets, "fake-head")
	beat()
	if reads() != first+1 {
		t.Fatalf("a hint in a heartbeat did not read the pull request: %d reads, %d before", reads(), first)
	}
	beat()
	beat()
	if reads() != first+1 {
		t.Fatalf("a hint was heard twice: %d reads, %d before", reads(), first)
	}
}

// A refusal for a rate limit holds the repository's reads, then longer each time, from a minute, and the notice of
// it is given once for the whole stretch.
func TestARateLimitedRepositoryIsHeldWithBackoffAndNoticedOnce(t *testing.T) {
	t.Parallel()
	f, reads, _ := ciRig(t)
	f.converge(2)
	base := reads()
	f.forge.Limit(ports.ForgeRate{RetryAfter: 30 * time.Second, Secondary: true})
	f.advance(11 * time.Minute)
	attempts := func() int { return reads() - base }
	if attempts() != 1 {
		t.Fatalf("a run past its p50 took %d attempts", attempts())
	}
	// Each wait is twice the one before, from a minute, and a retry-after under it does not shorten it.
	for _, wait := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute} {
		want := attempts()
		f.advance(wait - time.Second)
		if attempts() != want {
			t.Fatalf("the forge was asked again before the %s wait ended", wait)
		}
		f.advance(2 * time.Second)
		if attempts() != want+1 {
			t.Fatalf("the forge was not asked again after the %s wait", wait)
		}
	}
	if notices := f.notices(); len(notices) != 1 || !strings.Contains(notices[0], "rate limiting") || !strings.Contains(notices[0], "repo-1") {
		t.Errorf("the worker noticed %q, want one notice that names the repository", notices)
	}
	f.forge.Limit(ports.ForgeRate{})
	f.advance(9 * time.Minute)
	if attempts() != 5 {
		t.Fatalf("a limit that lifted left %d attempts", attempts())
	}
	f.forge.Limit(ports.ForgeRate{RetryAfter: time.Minute, Secondary: true})
	f.advance(10 * time.Minute)
	if notices := f.notices(); len(notices) != 2 {
		t.Errorf("a second stretch of limits left %d notices: %q", len(notices), notices)
	}
}

// A window with nothing left holds the reads until the reset the response names, and a hint does not break the hold.
func TestAnExhaustedWindowHoldsReadsUntilItsReset(t *testing.T) {
	t.Parallel()
	f, reads, _ := ciRig(t)
	f.converge(2)
	base := reads()
	f.forge.Limit(ports.ForgeRate{Exhausted: true, Reset: f.clock.Now().Add(25 * time.Minute)})
	f.advance(11 * time.Minute)
	if reads()-base != 1 {
		t.Fatalf("the first attempt left %d", reads()-base)
	}
	f.forge.Limit(ports.ForgeRate{})
	f.advance(10 * time.Minute)
	f.subject.NoteCIHint(widgets, "")
	f.advance(2 * time.Minute)
	if reads()-base != 1 {
		t.Fatalf("the forge was asked again before the window reset: %d attempts", reads()-base)
	}
	f.advance(3 * time.Minute)
	if reads()-base != 2 {
		t.Fatalf("the forge was not asked once the window reset: %d attempts", reads()-base)
	}
}

// x-poll-interval is a floor on how soon the repository is read on its schedule, and a hint reads at once through it.
func TestAPollIntervalIsAFloorTheScheduleKeepsAndAHintBreaks(t *testing.T) {
	t.Parallel()
	f, reads, _ := ciRig(t)
	f.converge(2)
	f.forge.PollEvery(20 * time.Minute)
	f.advance(11 * time.Minute)
	base := reads()
	for range 3 {
		f.advance(4 * time.Minute)
	}
	if reads() != base {
		t.Fatalf("the schedule read inside the poll interval: %d reads, %d before", reads(), base)
	}
	f.subject.NoteCIHint(widgets, "")
	f.advance(time.Second)
	if reads() != base+1 {
		t.Fatalf("a hint did not read through the poll interval: %d reads, %d before", reads(), base)
	}
	f.advance(21 * time.Minute)
	if reads() <= base+1 {
		t.Fatalf("the schedule never read once the poll interval passed: %d reads", reads())
	}
}
