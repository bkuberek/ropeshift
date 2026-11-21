package server_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/test/apitest"
)

// Two planning branches that propose the same version of one plan each raise a request of their own, and approving
// each approves the merge of its own branch, so each card names its branch and its work order. A card no other request
// shares a version with names none.
func TestGoldenPlanCardsThatShareAVersionNameTheirBranches(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	source := retrySource("The first draft.")
	aPlanLaneOn(t, d, "wo-a", 1, laneHead, "plan/retry-a")
	proposePlans(t, d, source)
	alone := pendingPlanRequests(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if len(alone) != 1 || alone[0].Branch != nil {
		t.Fatalf("a request no other shares a version with reads %+v", alone)
	}
	// The second branch asks later, so the view lists the cards in the order they were asked.
	clock.Advance(time.Minute)
	aPlanLaneOn(t, d, "wo-b", 1, secondPlanHead, "plan/retry-b")
	proposePlans(t, d, source)
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	needs := pendingPlanRequests(t, view)
	want := map[string]planning.PlanBranch{
		"wo-a": {WorkOrder: "wo-a", Branch: "plan/retry-a", Commit: laneHead},
		"wo-b": {WorkOrder: "wo-b", Branch: "plan/retry-b", Commit: secondPlanHead},
	}
	if len(needs) != 2 {
		t.Fatalf("Needs you holds %d plan approvals, want one for each branch", len(needs))
	}
	for _, need := range needs {
		if need.Branch == nil || *need.Branch != want[need.Approval.WorkOrder] || need.Version != 1 {
			t.Errorf("the card of %s names %+v, want %+v", need.Approval.WorkOrder, need.Branch, want[need.Approval.WorkOrder])
		}
	}
	holdsGolden(t, "needs-you-shared-version", steady(pretty(t, view)))
}
