package worker

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A review lane that began anywhere but the commit it judges would read the wrong work and call it the
// right, so it does not start.
func TestAReviewLaneNeedsTheCommitItJudges(t *testing.T) {
	const commit = "c0ffee0000000000000000000000000000000001"
	tests := []struct {
		name    string
		lease   ledger.DesiredLease
		refused bool
	}{
		{"a review that begins at the commit", ledger.DesiredLease{WorkOrder: "rv", Kind: "review", BaseCommit: commit, Attempt: &ledger.DesiredAttempt{Commit: commit}}, false},
		{"a review told nothing of what it judges", ledger.DesiredLease{WorkOrder: "rv", Kind: "review", BaseCommit: commit}, true},
		{"a review of no commit", ledger.DesiredLease{WorkOrder: "rv", Kind: "review", Attempt: &ledger.DesiredAttempt{}}, true},
		{"a review whose claim named no start", ledger.DesiredLease{WorkOrder: "rv", Kind: "review", Attempt: &ledger.DesiredAttempt{Commit: commit}}, true},
		{"a review that begins elsewhere", ledger.DesiredLease{WorkOrder: "rv", Kind: "review", BaseCommit: "another", Attempt: &ledger.DesiredAttempt{Commit: commit}}, true},
		{"a change needs none of it", ledger.DesiredLease{WorkOrder: "wo", Kind: "change"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := (&laneRun{lease: tt.lease}).judgeable(); (err != nil) != tt.refused {
				t.Errorf("got %v, refused is %v", err, tt.refused)
			}
		})
	}
}

// What a lane is held to follows the kind its work order names.
func TestARoleFollowsTheKindOfWork(t *testing.T) {
	tests := []struct {
		lease ledger.DesiredLease
		want  string
	}{
		{ledger.DesiredLease{WorkOrder: "wo", Kind: "change"}, lanes.RoleWriter},
		{ledger.DesiredLease{WorkOrder: "wo", Kind: "rework"}, lanes.RoleWriter},
		{ledger.DesiredLease{WorkOrder: "wo", Kind: "plan"}, lanes.RolePlanner},
		{ledger.DesiredLease{WorkOrder: "rv", Kind: "review"}, lanes.RoleReviewer},
		{ledger.DesiredLease{Lane: "orchestrator-1"}, lanes.RoleOrchestrator},
	}
	for _, tt := range tests {
		if got := (&laneRun{lease: tt.lease}).role(); got != tt.want {
			t.Errorf("a %q lane is a %s, want %s", tt.lease.Kind, got, tt.want)
		}
	}
}
