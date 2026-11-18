package worker_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A base the forge will not let a worker read is held, and said once to the repository's worker. When the refusal stands
// for ledger.CIBaseUnreadReads reads of the pull request in a row it is a finding a person answers: one Needs you item
// and one notice, however many reads follow, and no lane, no rerun, and no read of a log (ADR-0037, FR-SPEND-22).
func TestAPersistentRefusalOfTheBaseRaisesOneItemAndStartsNoLane(t *testing.T) {
	t.Parallel()
	f, number := reviewedRig(t)
	sessions := len(f.driver.Specs())
	f.forge.FailBase(errors.New("the forge refused the base"))
	f.forge.SetChecks(number, f.finishedNow("unit", "failure", "811"))
	f.step(2 * time.Minute)
	if kinds := f.ciKinds(); len(kinds) != 0 {
		t.Fatalf("one refused read raised %v, and a refusal the next read answers is nothing to a person", kinds)
	}
	for range 12 {
		f.step(2 * time.Minute)
	}
	if kinds := f.ciKinds(); !slices.Equal(kinds, []string{ledger.CIItemBaseUnread}) {
		t.Fatalf("a refusal that stood for twelve reads raised %v, want one %s", kinds, ledger.CIItemBaseUnread)
	}
	f.assertEvents(ledger.EventCIFindingRaised, 1)
	pr, err := f.pullRequest()
	if err != nil || len(pr.Checks) != 1 || !pr.Checks[0].BaseUnreadPersists() {
		t.Fatalf("the ledger holds %+v: %v", pr.Checks, err)
	}
	if got := f.row().State; got != workorders.Integrating {
		t.Fatalf("a failure nobody can place sent the work to %s", got)
	}
	if f.reruns() != 0 || countPrefix(f.forge.Calls(), "log ") != 0 {
		t.Fatalf("a held check was rerun or read for its log: %v", f.forge.Calls())
	}
	f.noSession(sessions)
	// Once the base can be read, the failure is the code's: the item leaves and the work goes back, once.
	f.forge.FailBase(nil)
	f.settle()
	if kinds := f.ciKinds(); len(kinds) != 0 {
		t.Fatalf("a base that can be read again still raises %v", kinds)
	}
	if got := f.row().State; got != workorders.Ready {
		t.Fatalf("once the base was read, the failure left the work at %s", got)
	}
}

// A rate limit on the base's read is "cannot tell yet", and no refusal: the check is held and the repository's reads wait, but
// none of those counts toward the reads in a row that raise the finding. Only refusals that are no rate limit do.
func TestARateLimitedReadOfTheBaseIsNotCountedAsARefusal(t *testing.T) {
	t.Parallel()
	f, number := reviewedRig(t)
	sessions := len(f.driver.Specs())
	f.forge.FailBase(&ports.RateLimitedError{Rate: ports.ForgeRate{RetryAfter: time.Minute}})
	f.forge.SetChecks(number, f.finishedNow("unit", "failure", "814"))
	for range 40 {
		f.step(2 * time.Minute)
	}
	if kinds := f.ciKinds(); len(kinds) != 0 {
		t.Fatalf("a base the forge rate limited raised %v", kinds)
	}
	pr, err := f.pullRequest()
	if err != nil || len(pr.Checks) != 1 || !pr.Checks[0].BaseUnread || pr.Checks[0].BaseUnreadReads != 0 {
		t.Fatalf("the ledger holds %+v, want a held check that no refusal was counted for: %v", pr.Checks, err)
	}
	f.noSession(sessions)
	// Refusals that are no rate limit count from the first, whatever the rate limit held before.
	f.forge.FailBase(errors.New("the forge refused the base"))
	for range 80 {
		f.step(2 * time.Minute)
	}
	if kinds := f.ciKinds(); !slices.Equal(kinds, []string{ledger.CIItemBaseUnread}) {
		t.Fatalf("a refusal that stood raised %v, want one %s", kinds, ledger.CIItemBaseUnread)
	}
}

// A finding for a base that could not be read leaves when the head no longer holds a failed check it could not place, even
// while a rerun a member asked for has the head's checks running again, which a classification waits for.
func TestAnUnreadBaseFindingLeavesWhileARerunHasTheChecksRunning(t *testing.T) {
	t.Parallel()
	f, number := reviewedRig(t)
	f.forge.FailBase(errors.New("the forge refused the base"))
	f.forge.SetChecks(number, f.finishedNow("unit", "failure", "815"))
	for range 12 {
		f.step(2 * time.Minute)
	}
	if kinds := f.ciKinds(); !slices.Equal(kinds, []string{ledger.CIItemBaseUnread}) {
		t.Fatalf("a refusal that stood raised %v", kinds)
	}
	running := ledger.PullRequestCheck{Name: "unit", State: ledger.CheckPending, Status: ledger.CIRunInProgress, Required: true, StartedAt: f.clock.Now().UTC().Format(time.RFC3339)}
	f.forge.SetChecks(number, running)
	f.settle()
	if kinds := f.ciKinds(); len(kinds) != 0 {
		t.Fatalf("the checks run again and the head still raises %v", kinds)
	}
}

// A refusal the next read answers is never raised: the count of reads in a row starts again at the first read that can
// tell, so two refusals, an answer, and two more refusals raise nothing.
func TestARefusalTheNextReadAnswersRaisesNothing(t *testing.T) {
	t.Parallel()
	f, number := reviewedRig(t)
	f.forge.SetChecks(number, f.finishedNow("unit", "failure", "812"))
	f.forge.SetBase(widgets, "main", "base-tip-1", "unit")
	for range 2 {
		f.forge.FailBase(errors.New("the forge refused the base"))
		f.step(2 * time.Minute)
		f.step(2 * time.Minute)
		f.forge.FailBase(nil)
		f.step(2 * time.Minute)
	}
	for _, kind := range f.ciKinds() {
		if kind == ledger.CIItemBaseUnread {
			t.Fatalf("refusals that were answered between raised %s", kind)
		}
	}
	// The base was red when it could be read, which is its own finding, and the only one.
	raised := f.eventsData(ledger.EventCIFindingRaised)
	if len(raised) != 1 || !strings.Contains(raised[0], ledger.CIItemBaseRed) {
		t.Fatalf("the findings raised were %v, want the red base alone", raised)
	}
}
