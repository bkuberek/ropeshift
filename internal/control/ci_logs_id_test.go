package control

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

// F5: the read of a head's logs is named for the failure it reads. A rerun that fails again finishes at another time, so
// it is read again, and a later rework never takes the log of the attempt before it.
func TestTheLogsOfAHeadAreNamedForEachFailureOfIt(t *testing.T) {
	t.Parallel()
	check := func(name, run, done string) ledger.PullRequestCheck {
		return ledger.PullRequestCheck{Name: name, DetailsURI: "https://github.com/acme/widgets/actions/runs/" + run + "/job/1", CompletedAt: done}
	}
	first := []ledger.PullRequestCheck{check("unit", "9", "2026-01-02T09:00:00Z"), check("lint", "8", "2026-01-02T09:01:00Z")}
	same := []ledger.PullRequestCheck{first[1], first[0]}
	again := []ledger.PullRequestCheck{check("unit", "9", "2026-01-02T09:30:00Z"), check("lint", "8", "2026-01-02T09:01:00Z")}
	other := []ledger.PullRequestCheck{check("unit", "10", "2026-01-02T09:00:00Z"), check("lint", "8", "2026-01-02T09:01:00Z")}
	id := logsID("wo-1", "head-1", first)
	if !strings.HasPrefix(id, "logs:wo-1:head-1:") {
		t.Errorf("the read is named %q", id)
	}
	if got := logsID("wo-1", "head-1", same); got != id {
		t.Errorf("the same failure in another order is named %q, not %q", got, id)
	}
	if got := logsID("wo-1", "head-1", again); got == id {
		t.Errorf("a failure that came again is named as the one before it, %q", got)
	}
	if got := logsID("wo-1", "head-1", other); got == id {
		t.Errorf("a failure of another run is named as the one before it, %q", got)
	}
}
