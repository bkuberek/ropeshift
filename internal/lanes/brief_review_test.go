package lanes_test

import (
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
