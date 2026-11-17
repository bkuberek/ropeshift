package server_test

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// A review of a finished lane is ready work, and a member starts it as any ready work: claim it for a worker,
// then ask the worker for a lane (FR-UI-7). The plans view lists it, with the change it judges and what a
// member may do to it, so the web UI offers the same Start the CLI's `work claim` and `lane start` give.
func TestAReadyReviewIsListedAndStartsFromTheWebRoutes(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Running("wo-judged", "Trim the log").End(workorders.OutcomeCompleted, "")

	plans := readView[api.Plans](t, d, api.ViewPlans, "")
	var review workorders.WorkOrder
	for _, order := range plans.Reviews {
		if order.Reviews == "wo-judged" {
			review = order
		}
	}
	if review.ID == "" {
		t.Fatalf("the plans view lists no review of wo-judged among %+v", plans.Reviews)
	}
	if review.Kind != workorders.KindReview || review.State != workorders.Ready {
		t.Fatalf("the review is %s work in %s", review.Kind, review.State)
	}
	if got := plans.WorkActions[review.ID].Actions; !slices.Equal(got, []string{"claim", "cancel"}) {
		t.Fatalf("a ready review allows %v", got)
	}
	for _, order := range plans.Unplanned {
		if order.ID == review.ID {
			t.Errorf("the review %s is listed as unplanned too", review.ID)
		}
	}

	if _, err := d.Client.Claim(t.Context(), review.ID, api.ClaimRequest{ClaimID: "claim-review", WorkerID: apitest.WorldWorker}); err != nil {
		t.Fatalf("a member claims the review: %v", err)
	}
	if got := readView[api.Plans](t, d, api.ViewPlans, "").WorkActions[review.ID].Actions; !slices.Equal(got, []string{"start_lane", "release", "cancel"}) {
		t.Fatalf("a claimed review allows %v", got)
	}
	if _, err := d.Client.StartLane(t.Context(), review.ID, api.StartLane{LaneID: "lane-review"}); err != nil {
		t.Fatalf("a member starts the review's lane: %v", err)
	}
}

// A review that ended is read with the work it judged, whose standing lists it, so it stays out of the list of
// reviews that wait or run.
func TestTheReviewsTheViewListsAreTheOnesThatHaveNotEnded(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Running("wo-waits", "Trim the log").End(workorders.OutcomeCompleted, "")
	w.Running("wo-ended", "Rotate the keys").End(workorders.OutcomeCompleted, "")
	w.Reviewed("wo-ended", workorders.CriterionVerdict{Criterion: "c1", Verdict: workorders.VerdictPass, Reasons: "It does."})

	listed := map[string]bool{}
	for _, order := range readView[api.Plans](t, d, api.ViewPlans, "").Reviews {
		listed[order.Reviews] = true
		if workorders.Terminal(order.State) {
			t.Errorf("the review %s of %s is listed in %s", order.ID, order.Reviews, order.State)
		}
	}
	if !listed["wo-waits"] {
		t.Error("the review that waits is not listed")
	}
	if listed["wo-ended"] {
		t.Error("the review that ended is listed")
	}
}
