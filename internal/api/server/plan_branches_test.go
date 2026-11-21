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

// A branch that only finishes the goal of a plan held as a placeholder raises no request of its own: Needs you asks for
// its work order's acceptance beside the held request. While both wait, the held request names the branch that finishes
// it, and once that work order no longer waits for acceptance it names none.
func TestGoldenAHeldPlanPointsToTheBranchThatFinishesIt(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	files := templatePlanFiles(t)
	planTitled(files, "Harbor berth booking")
	source := sourceOf(files)
	readSource(t, d, source)
	if held := pendingPlanRequests(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")); len(held) != 1 || held[0].FinishedBy != nil {
		t.Fatalf("the held request names a branch before any finishes it: %+v", held)
	}
	goalWritten(files)
	source.Write("plan/goal.md", []byte(files["plan/goal.md"]))
	clock.Advance(time.Minute)
	aPlanLaneOn(t, d, "wo-plan", 1, laneHead, "plan/harbor")
	proposePlans(t, d, source)
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	held := pendingPlanRequests(t, view)
	want := planning.PlanBranch{WorkOrder: "wo-plan", Branch: "plan/harbor", Commit: laneHead}
	if len(held) != 1 || len(held[0].Unfinished) == 0 || held[0].FinishedBy == nil || *held[0].FinishedBy != want {
		t.Fatalf("the held request reads %+v, want it to name %+v", held, want)
	}
	if !waitsForAcceptance(view, "wo-plan") {
		t.Fatal("Needs you does not ask for the acceptance of the branch that finishes the plan")
	}
	holdsGolden(t, "needs-you-held-plan-finishing", steady(pretty(t, view)))
	// Sent back, the branch no longer waits to be accepted, so the held request points to nothing.
	if _, err := d.Client.Rework(t.Context(), "wo-plan", api.ReworkRequest{Reason: "Name the berths."}); err != nil {
		t.Fatal(err)
	}
	if after := pendingPlanRequests(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")); len(after) != 1 || after[0].FinishedBy != nil {
		t.Errorf("the held request names a branch sent back: %+v", after)
	}
}
