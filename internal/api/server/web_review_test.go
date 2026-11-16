package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Starting a contract review from the browser is the API's own route, behind the web listener's session and
// token (FR-UI-7). These hold what the web UI relies on: a member's session starts one, the review reads
// the plan the work belongs to and stands ready for a member to claim, and the API's refusals are the words
// the page shows.

func TestAMemberStartsAContractReviewFromTheBrowser(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	b.send(t, http.MethodPost, "/v1/work-orders", api.CreateWorkOrder{
		ID: "wo-plan", Title: "Export the invoices", RepositoryID: "repo-1", Criteria: []planning.Criterion{{ID: "c1", Text: "An export downloads."}},
	}, http.StatusCreated)
	plan := planOfWork(t, b.r.d, "wo-plan")
	raw := b.send(t, http.MethodPost, "/v1/plans/"+plan+"/reviews", api.ReviewPlan{ID: "review-web"}, http.StatusCreated)
	var review workorders.WorkOrder
	if err := json.Unmarshal(raw, &review); err != nil {
		t.Fatal(err)
	}
	if review.ID != "review-web" || review.Kind != workorders.KindContractReview || review.ReviewsPlan != plan || review.State != workorders.Ready || review.RepositoryID != "repo-1" {
		t.Fatalf("the review is %+v", review)
	}
	// A repeat under one ID is the review the first made, so a page may send it again after a lost reply.
	b.send(t, http.MethodPost, "/v1/plans/"+plan+"/reviews", api.ReviewPlan{ID: "review-web"}, http.StatusCreated)
	list, err := b.r.d.Client.WorkOrders(t.Context(), workorders.Ready)
	if err != nil || len(list) != 1 {
		t.Fatalf("the project holds %d ready work orders, want the review alone: %v", len(list), err)
	}
}

func TestTheLedgerRefusesAReviewTheBrowserAsksForWrongly(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	// A plan whose work names no repository has nowhere for a review lane to read, and the page shows why.
	b.send(t, http.MethodPost, "/v1/work-orders", api.CreateWorkOrder{
		ID: "wo-bare", Title: "Export the invoices", Criteria: []planning.Criterion{{ID: "c1", Text: "An export downloads."}},
	}, http.StatusCreated)
	plan := planOfWork(t, b.r.d, "wo-bare")
	wantCode(t, b.send(t, http.MethodPost, "/v1/plans/"+plan+"/reviews", api.ReviewPlan{ID: "review-bare"}, http.StatusBadRequest), api.CodeInvalid)
	wantCode(t, b.send(t, http.MethodPost, "/v1/plans/"+plan+"/reviews", api.ReviewPlan{}, http.StatusBadRequest), api.CodeInvalid)
	wantCode(t, b.send(t, http.MethodPost, "/v1/plans/no-such-plan/reviews", api.ReviewPlan{ID: "review-none"}, http.StatusNotFound), api.CodeNotFound)
	list, err := b.r.d.Client.WorkOrders(t.Context(), workorders.Ready)
	if err != nil || len(list) != 0 {
		t.Fatalf("a refused review left %+v: %v", list, err)
	}
}
