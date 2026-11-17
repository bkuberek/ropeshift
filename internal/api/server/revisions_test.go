package server_test

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// anApprovedPlanWithADecision approves the retry plan and records a decision of a member about it, which leaves the
// plan needing revision.
func anApprovedPlanWithADecision(t *testing.T) (*apitest.Daemon, *apitest.World, string) {
	t.Helper()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Planned(retrySource("The first draft."))
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if _, err := d.Client.DecideApproval(t.Context(), need.Approval.ID, api.DecideApproval{Digest: need.Approval.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	plans, err := d.Client.Plans(t.Context())
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans %+v: %v", plans, err)
	}
	if _, err := d.Client.RecordDecision(t.Context(), api.RecordDecision{
		ID: "d-logo", Status: questions.DecisionDecided, Statement: "Use the wordmark.", Rationale: "It reads at small sizes.", Citations: []string{"brand-guide"},
		Scope: "plan:" + plans[0].ID, Reversibility: questions.Reversible, Categories: []string{questions.CategoryProduct},
	}); err != nil {
		t.Fatal(err)
	}
	return d, w, plans[0].ID
}

func refusal(t *testing.T, err error) api.ErrorDetail {
	t.Helper()
	var refused *api.Error
	if !errors.As(err, &refused) {
		t.Fatalf("error %v is no refusal of the API", err)
	}
	return refused.Detail
}

func TestAMembersDecisionInsideAnApprovedPlanShowsAsItsRevision(t *testing.T) {
	t.Parallel()
	d, _, plan := anApprovedPlanWithADecision(t)
	view, err := d.Client.PlanRevision(t.Context(), plan)
	if err != nil || view.Revision == nil || view.Revision.ApprovedVersion != 1 || view.Title != "Retry budget" {
		t.Fatalf("revision %+v: %v", view, err)
	}
	if len(view.Decisions) != 1 || view.Decisions[0].ID != "d-logo" || view.Decisions[0].Statement != "Use the wordmark." ||
		view.Decisions[0].Scope != "plan:"+plan || view.Decisions[0].Reversibility != questions.Reversible {
		t.Errorf("decisions %+v", view.Decisions)
	}
	if len(view.Waiting) != 1 || view.Waiting[0].Title != "Add the budget" {
		t.Errorf("work that waits: %+v", view.Waiting)
	}
	shown, err := d.Client.Plan(t.Context(), plan)
	if err != nil || shown.Revision == nil || !slices.Equal(shown.Revision.Revision.DecisionIDs, []string{"d-logo"}) {
		t.Errorf("the plan view says nothing of its revision: %+v: %v", shown.Revision, err)
	}
	if _, err := d.Client.PlanRevision(t.Context(), "absent"); refusal(t, err).Code != api.CodeNotFound {
		t.Errorf("a plan nobody has: %v", err)
	}
}

func TestAPlanWithNoDecisionsSinceItsApprovalHasNoRevision(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Planned(retrySource("The first draft."))
	plans, err := d.Client.Plans(t.Context())
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans %+v: %v", plans, err)
	}
	view, err := d.Client.PlanRevision(t.Context(), plans[0].ID)
	if err != nil || view.Revision != nil || len(view.Decisions) != 0 || view.Title != "Retry budget" {
		t.Errorf("revision %+v: %v", view, err)
	}
}

func TestConfirmingARevisionClosesItAndReleasesTheWorkThatWaited(t *testing.T) {
	t.Parallel()
	d, _, plan := anApprovedPlanWithADecision(t)
	closed, err := d.Client.ConfirmRevision(t.Context(), plan)
	if err != nil || closed.Open() || closed.Outcome != "confirmed" || closed.ClosedBy != "member:member-1" {
		t.Fatalf("confirmed %+v: %v", closed, err)
	}
	orders, err := d.Client.WorkOrders(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, order := range orders {
		if order.Title == "Add the budget" && order.State != workorders.Ready {
			t.Errorf("the work that waited is %s", order.State)
		}
	}
	if view, err := d.Client.PlanRevision(t.Context(), plan); err != nil || view.Revision != nil {
		t.Errorf("the plan still has %+v: %v", view.Revision, err)
	}
	if _, err := d.Client.ConfirmRevision(t.Context(), plan); refusal(t, err).Code != api.CodeNotFound {
		t.Errorf("a second confirm: %v", err)
	}
}

func TestRevisingARevisionRecordsAPlanningSessionWhoseBriefNamesTheDecisions(t *testing.T) {
	t.Parallel()
	d, _, plan := anApprovedPlanWithADecision(t)
	order, err := d.Client.ReviseSession(t.Context(), plan, api.ReviseSession{ID: "revise-1"})
	if err != nil || order.ID != "revise-1" || order.Kind != workorders.KindPlan || order.State != workorders.ContractReview || order.Title != "Revise Retry budget" {
		t.Fatalf("session %+v: %v", order, err)
	}
	again, err := d.Client.ReviseSession(t.Context(), plan, api.ReviseSession{ID: "revise-1"})
	if err != nil || again.ID != order.ID {
		t.Errorf("a retry made %+v: %v", again, err)
	}
	if _, err := d.Client.ReviseSession(t.Context(), plan, api.ReviseSession{ID: "not a plain id"}); refusal(t, err).Code != api.CodeInvalid {
		t.Errorf("an ID that is not plain: %v", err)
	}
	if _, err := d.Client.ConfirmRevision(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.ReviseSession(t.Context(), plan, api.ReviseSession{ID: "revise-2"}); refusal(t, err).Code != api.CodeNotFound {
		t.Errorf("revising a plan with no revision: %v", err)
	}
}

func TestDecisionReworkRefusesWorkThatHasNotFinishedAndAnEmptyRequest(t *testing.T) {
	t.Parallel()
	d, _, _ := anApprovedPlanWithADecision(t)
	orders, err := d.Client.WorkOrders(t.Context(), "")
	if err != nil || len(orders) == 0 {
		t.Fatalf("work orders %+v: %v", orders, err)
	}
	if _, err := d.Client.DecisionRework(t.Context(), orders[0].ID, api.DecisionRework{Decisions: []string{"d-logo"}}); refusal(t, err).Code != api.CodeInvalid {
		t.Errorf("work that has not finished: %v", err)
	}
	if _, err := d.Client.DecisionRework(t.Context(), orders[0].ID, api.DecisionRework{}); refusal(t, err).Code != api.CodeInvalid {
		t.Errorf("no decision: %v", err)
	}
	if _, err := d.Client.DecisionRework(t.Context(), "absent", api.DecisionRework{Decisions: []string{"d-logo"}}); refusal(t, err).Code != api.CodeNotFound {
		t.Errorf("work nobody has: %v", err)
	}
}

func TestRevisionRoutesTakeNoCallWithNoMemberSession(t *testing.T) {
	t.Parallel()
	d, _, plan := anApprovedPlanWithADecision(t)
	anonymous := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer anonymous.Close()
	for _, call := range []struct {
		path string
		body any
	}{
		{"/v1/plans/" + plan + "/revision/confirm", struct{}{}},
		{"/v1/plans/" + plan + "/revision/sessions", api.ReviseSession{ID: "revise-1"}},
		{"/v1/work-orders/any/decision-rework", api.DecisionRework{Decisions: []string{"d-logo"}}},
	} {
		err := anonymous.Do(t.Context(), http.MethodPost, apitest.Route(call.path), call.body, nil)
		refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
	}
	if view, err := d.Client.PlanRevision(t.Context(), plan); err != nil || view.Revision == nil {
		t.Errorf("a refused call changed the revision: %+v: %v", view.Revision, err)
	}
}
