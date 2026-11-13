package server_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

const retryPlan = `+++
kind = "plan"
id = "retry"
title = "Retry budget"

[[decision]]
id = "d1"
question = "Which provider first?"
answer = "Stripe"
rationale = "It is the one with an account."
+++

How.
`

func retrySource(spec string) *fake.WorkSource {
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(retryPlan))
	source.Write("spec.md", []byte("+++\nkind = \"spec\"\nid = \"api\"\ntitle = \"The API\"\nplan = \"retry\"\n+++\n\n"+spec+"\n"))
	source.Write("budget.md", []byte(`+++
kind = "contract"
id = "budget"
title = "The budget"
plan = "retry"

[[criteria]]
id = "c1"
text = "It retries"

[[work_orders]]
id = "budget-order"
title = "Add the budget"
size = "small"
+++

The budget.
`))
	return source
}

func planApprovalNeed(t *testing.T, view api.NeedsYou) *api.PlanApprovalNeed {
	t.Helper()
	for _, item := range view.Items {
		if item.Kind == api.NeedPlanApproval {
			if item.PlanApproval == nil {
				t.Fatalf("a plan approval carries nothing: %+v", item)
			}
			return item.PlanApproval
		}
	}
	t.Fatalf("no plan approval among %+v", view.Items)
	return nil
}

func TestNeedsYouHoldsAPlanApprovalWithWhatItHoldsAndHowItDiffers(t *testing.T) {
	d, w, _ := aWorld(t)
	w.Planned(retrySource("The first draft."))
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	need := planApprovalNeed(t, view)
	if need.Title != "Retry budget" || need.Version != 1 || need.RequestedBy != "system:intake" || need.Decisions != 1 ||
		need.Approval.SubjectDigest == "" || need.Approval.Kind != authority.KindPlan {
		t.Errorf("need %+v", need)
	}
	if need.Counts["contract"] != 1 || need.Counts["spec"] != 1 {
		t.Errorf("counts %v", need.Counts)
	}
	if need.Difference.Base != 0 || !slices.Equal(need.Difference.Added, []string{"contract:budget", "plan:retry", "spec:api"}) || need.Summary == "" {
		t.Errorf("difference %+v (%q)", need.Difference, need.Summary)
	}
	// The same request is not also a tool call a lane asked about, and the work its contract covers waits
	// for the plan and not for a review of its own.
	for _, item := range view.Items {
		if item.Kind == api.NeedApproval && item.Approval.Kind == authority.KindPlan {
			t.Errorf("the plan approval is also a lane's approval: %+v", item)
		}
		if item.Kind == api.NeedContractReview && item.ContractReview.WorkOrder.Title == "Add the budget" {
			t.Errorf("work a pending plan covers asks for its own review: %+v", item)
		}
	}
}

func TestNeedsYouHoldsAContractReviewNoPlanStandsBehind(t *testing.T) {
	d, w, _ := aWorld(t)
	w.Recorded("wo-alone", "Document the budget")
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	var found *api.ContractReviewNeed
	for _, item := range view.Items {
		if item.Kind == api.NeedContractReview && item.ContractReview.WorkOrder.ID == "wo-alone" {
			found = item.ContractReview
		}
	}
	if found == nil || found.Contract == "" || found.Version != 1 {
		t.Fatalf("contract review need %+v among %+v", found, view.Items)
	}
	if _, err := d.Client.Review(t.Context(), "wo-alone"); err != nil {
		t.Fatal(err)
	}
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind == api.NeedContractReview && item.ContractReview.WorkOrder.ID == "wo-alone" {
			t.Errorf("a reviewed work order still asks for a review: %+v", item)
		}
	}
}

func TestApprovingAPlanFromNeedsYouLeavesNothingToReview(t *testing.T) {
	d, w, _ := aWorld(t)
	w.Planned(retrySource("The first draft."))
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	approved, err := d.Client.DecideApproval(t.Context(), need.Approval.ID, api.DecideApproval{Digest: need.Approval.SubjectDigest, Allow: true})
	if err != nil {
		t.Fatal(err)
	}
	if approved.State != authority.ApprovalApproved || approved.DecidedBy != "member:member-1" {
		t.Fatalf("approval %+v", approved)
	}
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind == api.NeedPlanApproval || item.Kind == api.NeedContractReview {
			t.Errorf("%s stayed after the plan was approved: %+v", item.Kind, item)
		}
	}
	orders, err := d.Client.WorkOrders(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, order := range orders {
		if order.Title == "Add the budget" && (order.State != workorders.Ready || order.ReviewedBy != "member-1") {
			t.Errorf("the work order is %s, reviewed by %q", order.State, order.ReviewedBy)
		}
	}
}

func TestANewVersionAsksAgainAndShowsWhatChanged(t *testing.T) {
	d, w, _ := aWorld(t)
	source := retrySource("The first draft.")
	w.Planned(source)
	first := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if _, err := d.Client.DecideApproval(t.Context(), first.Approval.ID, api.DecideApproval{Digest: first.Approval.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	source.Write("spec.md", []byte("+++\nkind = \"spec\"\nid = \"api\"\ntitle = \"The API\"\nplan = \"retry\"\n+++\n\nThe second draft.\n"))
	w.Planned(source)
	second := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if second.Version != 2 || second.Difference.Base != 1 || !slices.Equal(second.Difference.Changed, []string{"spec:api"}) ||
		len(second.Difference.Added)+len(second.Difference.Removed) != 0 || !strings.Contains(second.Summary, "1 changed") {
		t.Errorf("version 2 differs by %+v (%q)", second.Difference, second.Summary)
	}
	// The work under the contract nothing changed stays as it was.
	orders, err := d.Client.WorkOrders(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, order := range orders {
		if order.Title == "Add the budget" && order.State != workorders.Ready {
			t.Errorf("an unchanged contract's work went to %s", order.State)
		}
	}
}

func TestShowingAPlanCarriesItsApprovalAndTheDifference(t *testing.T) {
	d, w, _ := aWorld(t)
	w.Planned(retrySource("The first draft."))
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	view, err := d.Client.Plan(t.Context(), need.Approval.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if view.Approval == nil || view.Approval.Approval.ID != need.Approval.ID || view.Approval.Detail.Title != "Retry budget" ||
		len(view.Approval.Drafts) != 1 || view.Approval.Drafts[0].Answer != "Stripe" || len(view.Approval.Records) != 3 {
		t.Fatalf("approval %+v", view.Approval)
	}
}

// A member decides the drafts with the plan, so Needs you shows each one's question and answer.
func TestAPlanApprovalNeedCarriesItsDraftDecisions(t *testing.T) {
	d, w, _ := aWorld(t)
	w.Planned(retrySource("The first draft."))
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if len(need.Drafts) != 1 || need.Drafts[0].ID != "d1" || need.Drafts[0].Question != "Which provider first?" ||
		need.Drafts[0].Answer != "Stripe" || need.Drafts[0].Rationale == "" {
		t.Errorf("drafts %+v", need.Drafts)
	}
}

// A contract review names the plan its contract belongs to, so a person can read the contract first.
func TestAContractReviewNeedNamesItsPlan(t *testing.T) {
	d, w, _ := aWorld(t)
	w.Recorded("wo-alone", "Document the budget")
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind == api.NeedContractReview && item.ContractReview.WorkOrder.ID == "wo-alone" {
			if item.ContractReview.Plan == "" || item.ContractReview.Plan != w.PlanOf("wo-alone") {
				t.Errorf("the review names plan %q, want %q", item.ContractReview.Plan, w.PlanOf("wo-alone"))
			}
			return
		}
	}
	t.Fatal("no contract review for wo-alone")
}

// A member passes a contract review, or sends the work back, from the browser as from the CLI
// (FR-UI-6).
func TestAWebPagePassesOrSendsBackAContractReview(t *testing.T) {
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Recorded("wo-pass", "Pass this one")
	w.Recorded("wo-back", "Send this one back")
	r := webOn(t, d)
	cookies, token := r.signedIn(t)
	headers := map[string]string{api.HeaderCSRF: token, "Origin": "http://" + r.web.Host}
	response, raw := r.send(t, call{method: http.MethodPost, path: apitest.Route("/v1/work-orders/wo-pass/review"), body: struct{}{}, cookies: cookies, headers: headers})
	wantStatus(t, response, raw, http.StatusOK, "")
	response, raw = r.send(t, call{
		method: http.MethodPost, path: apitest.Route("/v1/work-orders/wo-back/send-back"), body: api.SendBack{Reason: "The criteria are vague."}, cookies: cookies, headers: headers,
	})
	wantStatus(t, response, raw, http.StatusNoContent, "")
	states := map[string]workorders.State{}
	orders, err := d.Client.WorkOrders(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, order := range orders {
		states[order.ID] = order.State
	}
	if states["wo-pass"] != workorders.Ready || states["wo-back"] != workorders.Planning {
		t.Errorf("states %v", states)
	}
}

// ADR-0023: Needs you shows an adopted plan's request grouped as its detail holds it, so the web UI lists
// every claim with its evidence, each continued branch, what is new, what is left out, and the branch's
// changes.
func TestAPlanApprovalNeedCarriesWhatAnAdoptionGroups(t *testing.T) {
	d, _, _ := aWorld(t)
	adoption := planning.Adoption{
		Exists:    []planning.ExistingRecord{{Ref: "requirement:SHOP-1", Title: "One client", Evidence: []string{"shop:client.go"}}},
		Continues: []planning.ContinuedWork{{Contract: "contract:retry", WorkOrder: "retry-finish", Title: "Finish it", Branch: "feature/retry"}},
		New:       []planning.NewRecord{},
		LeftOut:   []planning.LeftOut{{Ref: "branch:spike/old-ui", Reason: "Abandoned"}},
		Changes:   []planning.FileChange{{Status: planning.ChangeAdded, Path: "plans/plan.md"}},
	}
	detail, err := json.Marshal(planning.PlanDetail{Title: "Adopt shop", Ref: "shop", Adoption: &adoption})
	if err != nil {
		t.Fatal(err)
	}
	request := authority.Approval{
		ID: "adopt-approval", Kind: authority.KindPlan, WorkOrder: "adopt-1", Subject: "shop@1", SubjectDigest: "sha256:abc", Summary: "Plan Adopt shop, version 1",
		RequestedBy: "lane:lane-1", State: authority.ApprovalPending, RaisedAt: 1, PlanVersion: 1, Detail: string(detail),
	}
	err = d.Store.Write(t.Context(), apitest.Scope, "test.approval", func(tx ports.WriteTx) error {
		if _, err := tx.Approvals().Insert(request); err != nil {
			return err
		}
		event, err := authority.ApprovalEvent(request, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if need.Adoption == nil || !reflect.DeepEqual(*need.Adoption, adoption) {
		t.Errorf("the need groups %+v, want %+v", need.Adoption, adoption)
	}
}

// approvedThenChanged is a plan whose first version a member approved and whose second waits.
func approvedThenChanged(t *testing.T) (*apitest.Daemon, string) {
	t.Helper()
	d, w, _ := aWorld(t)
	source := retrySource("The first draft.")
	w.Planned(source)
	first := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if _, err := d.Client.DecideApproval(t.Context(), first.Approval.ID, api.DecideApproval{Digest: first.Approval.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	source.Write("spec.md", []byte("+++\nkind = \"spec\"\nid = \"api\"\ntitle = \"The API\"\nplan = \"retry\"\n+++\n\nThe second draft.\n"))
	w.Planned(source)
	second := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	return d, second.Approval.Plan
}

func TestShowingAPlanListsEachVersionWithItsApprovalState(t *testing.T) {
	d, plan := approvedThenChanged(t)
	view, err := d.Client.Plan(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Versions) != 2 {
		t.Fatalf("versions %+v", view.Versions)
	}
	newest, oldest := view.Versions[0], view.Versions[1]
	if newest.Version != 2 || newest.State != authority.ApprovalPending || newest.Approval == "" || newest.Digest == "" {
		t.Errorf("newest %+v", newest)
	}
	if oldest.Version != 1 || oldest.State != authority.ApprovalApproved || oldest.Approval == "" || oldest.Digest == newest.Digest {
		t.Errorf("oldest %+v", oldest)
	}
	if view.Approval == nil || view.Approval.Approval.PlanVersion != 2 {
		t.Errorf("with no version the answer stays the open request: %+v", view.Approval)
	}
}

func TestShowingAPlanByVersionReadsThatVersionsApproval(t *testing.T) {
	d, plan := approvedThenChanged(t)
	first, err := d.Client.PlanVersion(t.Context(), plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Approval == nil || first.Approval.Approval.PlanVersion != 1 || first.Approval.Approval.State != authority.ApprovalApproved ||
		first.Approval.Approval.DecidedBy == "" || len(first.Approval.Drafts) != 1 || len(first.Versions) != 2 {
		t.Errorf("version 1 reads %+v", first.Approval)
	}
	second, err := d.Client.PlanVersion(t.Context(), plan, 2)
	if err != nil {
		t.Fatal(err)
	}
	if second.Approval == nil || second.Approval.Approval.PlanVersion != 2 || !second.Approval.Approval.Open() ||
		!slices.Equal(second.Approval.Detail.Difference.Changed, []string{"spec:api"}) {
		t.Errorf("version 2 reads %+v", second.Approval)
	}
}

func TestShowingAPlanVersionItNeverHadIsRefused(t *testing.T) {
	d, plan := approvedThenChanged(t)
	_, err := d.Client.PlanVersion(t.Context(), plan, 9)
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	var out api.PlanView
	err = d.Client.Do(t.Context(), http.MethodGet, d.Client.Route("/v1/plans/"+plan+"?version=zero"), nil, &out)
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	err = d.Client.Do(t.Context(), http.MethodGet, d.Client.Route("/v1/plans/"+plan+"?version=0"), nil, &out)
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
}
