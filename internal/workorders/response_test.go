package workorders

import "testing"

// TestWhatADecisionDoesToWorkByWhereItStands holds the table of ADR-0030: a decision that binds work acts on
// `change` and `rework` work orders alone, and each state has one response.
func TestWhatADecisionDoesToWorkByWhereItStands(t *testing.T) {
	want := map[State]Response{
		Intake: Unstarted, Planning: Unstarted, ContractReview: Unstarted, Ready: Unstarted,
		Claimed: Live, Running: Live, Parked: Live, Lapsed: Live, Blocked: Live,
		Acceptance: Finished, Integrating: Finished, Done: Finished,
		Failed: Untouched, Canceled: Untouched,
	}
	if len(want) != len(States()) {
		t.Fatalf("the table holds %d states, and there are %d", len(want), len(States()))
	}
	for _, kind := range []Kind{KindChange, KindRework} {
		for state, response := range want {
			if got := ResponseOf(WorkOrder{Kind: kind, State: state}); got != response {
				t.Errorf("a %s in %s is %q, want %q", kind, state, got, response)
			}
		}
	}
}

// TestADecisionNeverMovesAPlanAReviewOrAContractReview holds the rule the overturn of a review lane's
// decision broke: the work orders that read and judge are told through their lanes, and never moved or reworked.
func TestADecisionNeverMovesAPlanAReviewOrAContractReview(t *testing.T) {
	for _, kind := range []Kind{KindPlan, KindContractReview, KindReview} {
		for _, state := range States() {
			if got := ResponseOf(WorkOrder{Kind: kind, State: state}); got != Untouched {
				t.Errorf("a %s in %s is %q, want it untouched", kind, state, got)
			}
		}
	}
}
