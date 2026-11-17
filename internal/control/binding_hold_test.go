package control

import (
	"testing"

	"ropeshift.com/ropeshift/internal/workorders"
)

// TestADecisionReplacesOnlyAHoldThatIsAReadingOfFacts holds which start holds the hold for a decision may replace: the
// ones a claim and the tick read again. The hold for a decision, and a reason this table does not name, stand.
func TestADecisionReplacesOnlyAHoldThatIsAReadingOfFacts(t *testing.T) {
	tests := []struct {
		reason workorders.HoldReason
		want   bool
	}{
		{workorders.HoldWaitingForMerge, true},
		{workorders.HoldDependenciesDiverge, true},
		{workorders.HoldBaseUnavailable, true},
		{workorders.HoldNoCheckout, true},
		{workorders.HoldContinuedBranchMissing, true},
		{workorders.HoldPullRequestPending, true},
		{workorders.HoldForgeUnavailable, true},
		{workorders.HoldDecision, false},
		{"held_by_a_member", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			if got := derivedHold(&workorders.StartHold{Reason: tt.reason}); got != tt.want {
				t.Errorf("derivedHold(%s) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}
