package lanes_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The first prompt of a review lane is read by a model, so its words are held to a golden.
func TestAReviewBriefIsStable(t *testing.T) {
	brief := lanes.Brief(lanes.BriefSpec{
		WorkOrder: "rv-1", Title: "Review: Add the retry budget", Statement: "Retry a failed charge twice.",
		Criteria: []string{"c1: A failed charge is retried twice.", "c2: Each retry is logged."}, Pack: true, Kind: workorders.KindReview,
		Attempt: &ledger.DesiredAttempt{
			WorkOrder: "wo-1", Title: "Add the retry budget", Branch: "lane/lane-1", Commit: "c0ffee0000000000000000000000000000000001",
			BaseRef: "origin/main", BaseCommit: "ba5e000000000000000000000000000000000002",
		},
	})
	golden(t, "brief-review.golden", brief)
}

// The security points are a review's alone: a lane that writes the work is not asked to judge any (FR-PRAC-9).
func TestOnlyAReviewBriefNamesTheSecurityPoints(t *testing.T) {
	review := lanes.Brief(lanes.BriefSpec{
		WorkOrder: "rv-1", Title: "Review: Add the retry budget", Kind: workorders.KindReview,
		Attempt: &ledger.DesiredAttempt{WorkOrder: "wo-1", Branch: "lane/lane-1", Commit: "c0ffee"},
	})
	for _, want := range []string{
		"security points", "secrets", "injection", "dependencies", "authority", "`CODEOWNERS`", "A note is not a verdict",
		"you judge every criterion as you would without it",
	} {
		if !strings.Contains(review, want) {
			t.Errorf("the review brief leaves out %q", want)
		}
	}
	for _, kind := range []workorders.Kind{workorders.KindChange, workorders.KindRework, workorders.KindPlan} {
		if brief := lanes.Brief(lanes.BriefSpec{WorkOrder: "wo", Title: "Work", Kind: kind}); strings.Contains(brief, "security points") {
			t.Errorf("a %s brief names the security points:\n%s", kind, brief)
		}
	}
}
