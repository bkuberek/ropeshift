package server_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
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

// retryTwoDrafts is the retry plan with a second draft decision.
const retryTwoDrafts = `+++
kind = "plan"
id = "retry"
title = "Retry budget"

[[decision]]
id = "d1"
question = "Which provider first?"
answer = "Stripe"
rationale = "It is the one with an account."

[[decision]]
id = "d2"
question = "How many retries?"
answer = "Three"
rationale = "It is what the spec says."
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
	t.Parallel()
	d, w, _ := aWorld(t)
	w.Planned(retrySource("The first draft."))
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	need := planApprovalNeed(t, view)
	if need.Title != "Retry budget" || need.Version != 1 || need.RequestedBy != "system:intake" || need.Decisions != 1 ||
		need.Approval.SubjectDigest == "" || need.Approval.Kind != authority.KindPlan || need.Revision != nil {
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// The forecasts of a plan version name milestones by ID, so its approval carries each milestone's title for the
// page to read them by.
func TestAPlanApprovalNamesItsMilestonesByTitle(t *testing.T) {
	t.Parallel()
	d, w, _ := aWorld(t)
	source := retrySource("The first draft.")
	source.Write("m1.md", []byte("+++\nkind = \"milestone\"\nid = \"m1\"\ntitle = \"Reliable checkout\"\nplan = \"retry\"\npriority = 1\n+++\n\nWhat it delivers.\n"))
	source.Write("m2.md", []byte("+++\nkind = \"milestone\"\nid = \"m2\"\ntitle = \"Email receipts\"\nplan = \"retry\"\npriority = 2\n+++\n\nWhat it delivers.\n"))
	source.Write("budget.md", []byte("+++\nkind = \"contract\"\nid = \"budget\"\ntitle = \"The budget\"\nplan = \"retry\"\nmilestone = \"m1\"\n\n[[criteria]]\nid = \"c1\"\ntext = \"It retries\"\n\n[[work_orders]]\nid = \"budget-order\"\ntitle = \"Add the budget\"\nsize = \"small\"\n+++\n\nThe budget.\n"))
	w.Planned(source)
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	view, err := d.Client.Plan(t.Context(), need.Approval.Plan)
	if err != nil {
		t.Fatal(err)
	}
	want := []api.MilestoneTitle{{ID: "m1", Title: "Reliable checkout"}, {ID: "m2", Title: "Email receipts"}}
	if view.Approval == nil || !slices.Equal(view.Approval.Milestones, want) {
		t.Fatalf("milestones %+v, want %+v", view.Approval, want)
	}
}

// A plan with no milestone file names none.
func TestAPlanApprovalWithNoMilestoneNamesNone(t *testing.T) {
	t.Parallel()
	d, w, _ := aWorld(t)
	w.Planned(retrySource("The first draft."))
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	view, err := d.Client.Plan(t.Context(), need.Approval.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if view.Approval == nil || len(view.Approval.Milestones) != 0 {
		t.Fatalf("milestones %+v", view.Approval)
	}
}

// A member decides the drafts with the plan, so Needs you shows each one's question and answer.
func TestAPlanApprovalNeedCarriesItsDraftDecisions(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	askAboutABranch(t, d, "adopt-approval", "plan-1", detail)
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if need.Adoption == nil || !reflect.DeepEqual(*need.Adoption, adoption) {
		t.Errorf("the need groups %+v, want %+v", need.Adoption, adoption)
	}
}

// askAboutABranch raises the request a planning lane's branch raises for its plan's first version, which the
// ledger holds no record of until a member approves it, for the work order the lane ran.
func askAboutABranch(t *testing.T, d *apitest.Daemon, id, workOrder string, detail []byte) {
	t.Helper()
	raiseBranchRequest(t, d, authority.Approval{
		ID: id, Kind: authority.KindPlan, WorkOrder: workOrder, Subject: "shop@1", SubjectDigest: "sha256:abc", Summary: "Plan shop, version 1",
		RequestedBy: "lane:lane-1", State: authority.ApprovalPending, RaisedAt: 1, PlanVersion: 1, Detail: string(detail),
	})
}

// raiseBranchRequest records a request a planning lane's branch raised, with the event that says so.
func raiseBranchRequest(t *testing.T, d *apitest.Daemon, request authority.Approval) {
	t.Helper()
	err := d.Store.Write(t.Context(), apitest.Scope, "test.approval", func(tx ports.WriteTx) error {
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
}

// A first version from a planning lane's branch drafts the answers its request carries, because the ledger
// holds no version of the plan yet, so Needs you lists the same drafts that approving decides.
func TestAPlanApprovalNeedOfABranchCarriesTheDraftsItsRequestHolds(t *testing.T) {
	t.Parallel()
	d, _, _ := aWorld(t)
	drafts := []planning.DraftDecision{{ID: "sailings", Question: "Do the sailings get a page of their own?", Answer: "No.", Rationale: "The person said so."}}
	detail, err := json.Marshal(planning.PlanDetail{Title: "Berths", Ref: "harbor-v1", Decisions: len(drafts), Drafts: drafts})
	if err != nil {
		t.Fatal(err)
	}
	askAboutABranch(t, d, "branch-approval", "plan-1", detail)
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if !reflect.DeepEqual(need.Drafts, drafts) || need.Decisions != len(drafts) {
		t.Errorf("the need drafts %+v (%d decisions), want %+v", need.Drafts, need.Decisions, drafts)
	}
}

// A branch's second version of a plan the ledger holds is a version the ledger holds no record of until a member
// approves it, so its plan page reads the version from its request and does not answer not found.
func TestShowingAPlanReadsTheSecondVersionOfABranchTheLedgerHoldsNoRecordOf(t *testing.T) {
	d, w, _ := aWorld(t)
	w.Planned(retrySource("The first draft."))
	first := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if _, err := d.Client.DecideApproval(t.Context(), first.Approval.ID, api.DecideApproval{Digest: first.Approval.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	drafts := []planning.DraftDecision{{ID: "d2", Question: "How many retries?", Answer: "Three", Rationale: "It is what the spec says."}}
	detail, err := json.Marshal(planning.PlanDetail{Title: "Retry budget", Ref: "retry", Decisions: len(drafts), Drafts: drafts, Source: "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	raiseBranchRequest(t, d, authority.Approval{
		ID: "branch-v2", Kind: authority.KindPlan, Plan: first.Approval.Plan, WorkOrder: "plan-1", Subject: "retry@2", SubjectDigest: "sha256:second",
		Summary: "Plan retry, version 2", RequestedBy: "lane:lane-1", State: authority.ApprovalPending, RaisedAt: 2, PlanVersion: 2, Detail: string(detail),
	})
	reads := map[string]func() (api.PlanView, error){
		"with no version": func() (api.PlanView, error) { return d.Client.Plan(t.Context(), first.Approval.Plan) },
		"by its version":  func() (api.PlanView, error) { return d.Client.PlanVersion(t.Context(), first.Approval.Plan, 2) },
	}
	for name, read := range reads {
		view, err := read()
		if err != nil {
			t.Fatalf("the plan %s: %v", name, err)
		}
		if view.Approval == nil || view.Approval.Approval.ID != "branch-v2" || !view.Approval.Approval.Open() || !reflect.DeepEqual(view.Approval.Drafts, drafts) {
			t.Errorf("the plan %s reads %+v, want the branch's open request with its drafts", name, view.Approval)
		}
		if len(view.Versions) != 2 || view.Versions[0].Version != 2 || view.Versions[0].Approval != "branch-v2" {
			t.Errorf("the plan %s lists %+v", name, view.Versions)
		}
	}
}

// branchWaitingForAcceptance is a planning lane's work order that waits for acceptance, with the request its
// branch raised, read from the commit the lane ended on.
func branchWaitingForAcceptance(t *testing.T) (*apitest.Daemon, string) {
	t.Helper()
	d := apitest.Start(t)
	d.Completed(t, "wo-plan", laneEnd(workorders.CheckResult{Name: "unit", Criterion: "c1", Passed: true}))
	detail, err := json.Marshal(planning.PlanDetail{Title: "Berths", Ref: "harbor-v1", Source: "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	askAboutABranch(t, d, "branch-approval", "wo-plan", detail)
	return d, "branch-approval"
}

// waitsForAcceptance says whether Needs you asks a member to accept a work order.
func waitsForAcceptance(view api.NeedsYou, id string) bool {
	return slices.ContainsFunc(view.Items, func(item api.NeedItem) bool {
		return item.Work != nil && item.Work.Reason == api.ReasonAcceptance && item.Work.WorkOrder.ID == id
	})
}

// ADR-0018: approving the plan a planning lane's branch holds accepts the lane's work order, so Needs you asks
// for that one approval, and approving it leaves nothing of either.
func TestNeedsYouAsksOnlyForThePlanApprovalOfABranchWaitingForAcceptance(t *testing.T) {
	t.Parallel()
	d, id := branchWaitingForAcceptance(t)
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	need := planApprovalNeed(t, view)
	if waitsForAcceptance(view, "wo-plan") {
		t.Error("Needs you asks to accept the work order beside the plan approval that accepts it")
	}
	if _, err := d.Client.DecideApproval(t.Context(), id, api.DecideApproval{Digest: need.Approval.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	view = readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	if waitsForAcceptance(view, "wo-plan") || slices.ContainsFunc(view.Items, func(item api.NeedItem) bool { return item.ID == id }) {
		t.Errorf("approving left %+v", view.Items)
	}
	if row, err := d.Client.WorkOrder(t.Context(), "wo-plan"); err != nil || row.State == workorders.Acceptance {
		t.Errorf("the work order reads %+v: %v", row, err)
	}
}

// A plan approval raised for an earlier commit of the branch cannot accept the work order, which approving it
// refuses, so Needs you asks for the work order's acceptance beside it.
func TestNeedsYouAsksForTheAcceptanceOfABranchThatMovedPastItsPlanApproval(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	d.Completed(t, "wo-plan", laneEnd(workorders.CheckResult{Name: "unit", Criterion: "c1", Passed: true}))
	detail, err := json.Marshal(planning.PlanDetail{Title: "Berths", Ref: "harbor-v1", Source: "an-earlier-commit"})
	if err != nil {
		t.Fatal(err)
	}
	askAboutABranch(t, d, "stale-approval", "wo-plan", detail)
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	planApprovalNeed(t, view)
	if !waitsForAcceptance(view, "wo-plan") {
		t.Error("a plan approval of an earlier commit hides the work order's acceptance")
	}
}

// A declined plan leaves the lane's work order waiting, so Needs you asks for its acceptance again.
func TestNeedsYouAsksForTheAcceptanceOfABranchWhosePlanWasDeclined(t *testing.T) {
	t.Parallel()
	d, id := branchWaitingForAcceptance(t)
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if _, err := d.Client.DecideApproval(t.Context(), id, api.DecideApproval{Digest: need.Approval.SubjectDigest, Allow: false, Reason: "Per key."}); err != nil {
		t.Fatal(err)
	}
	if !waitsForAcceptance(readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""), "wo-plan") {
		t.Error("the declined plan's work order no longer waits in Needs you")
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

// derivedDecisionID is the ID plan approval gives the decision a draft becomes, which anyone who reads the plan
// can work out: from the plan, its version, and the draft's ID.
func derivedDecisionID(plan string, version int64, draft string) string {
	sum := sha256.Sum256([]byte(plan + "\x00" + strconv.FormatInt(version, 10) + "\x00" + draft))
	return "pd-" + hex.EncodeToString(sum[:])[:26]
}

// heldUnderDraftID writes a decision straight into the ledger, as a ledger written before only plan approval
// could name a decision under the prefix of the IDs it derives may hold one, recorded by the actor given.
func heldUnderDraftID(t *testing.T, d *apitest.Daemon, decision questions.Decision, by ledger.Actor) {
	t.Helper()
	err := d.Store.Write(t.Context(), apitest.Scope, "decision.record", func(tx ports.WriteTx) error {
		if err := tx.Questions().InsertDecision(decision); err != nil {
			return err
		}
		event, err := questions.RecordEvent(questions.EventDecisionRecorded, questions.SubjectDecision, decision.ID, decision, by)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestNoCallerNamesADecisionUnderThePlanApprovalPrefix holds the prefix of the IDs plan approval derives to plan
// approval: recording a decision, answering a question, and overturning a decision under an ID with that prefix
// answer 400, for a member as for a delegate, whose call is refused before it becomes a card.
func TestNoCallerNamesADecisionUnderThePlanApprovalPrefix(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	ctx := t.Context()
	delegate := asDelegate(t, d.Socket, aDelegate(t, d.Client.Client, apitest.ProjectName).Token, apitest.ProjectName)
	if _, err := d.Client.RecordQuestion(ctx, api.RecordQuestion{ID: "q1", Text: "How many retries?", Categories: []string{questions.CategoryProduct}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.RecordDecision(ctx, api.RecordDecision{ID: "d1", Status: questions.DecisionDecided, Statement: "Retry twice."}); err != nil {
		t.Fatal(err)
	}
	reserved := derivedDecisionID("retry", 2, "d1")
	within := api.RecordDecision{
		ID: reserved, Status: questions.DecisionDecided, Statement: "Retry twice.", Rationale: "The budget allows it.", Scope: "repo:retries",
		Reversibility: questions.Reversible, Citations: []string{"docs/retries.md"}, Categories: []string{questions.CategoryProduct},
	}
	for _, client := range []*api.ProjectClient{d.Client, delegate} {
		_, err := client.RecordDecision(ctx, within)
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		_, err = client.AnswerQuestion(ctx, "q1", api.AnswerQuestion{ID: reserved, Answer: "Retry twice."})
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		_, err = client.OverturnDecision(ctx, "d1", api.OverturnDecision{ID: reserved, Statement: "Retry three times."})
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	}
	_, err := d.Client.Decision(ctx, reserved)
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	if decision, err := d.Client.Decision(ctx, "d1"); err != nil || decision.Status != questions.DecisionDecided {
		t.Errorf("the decision a refused overturn named is %+v: %v", decision, err)
	}
}

// TestAPlanApprovalRefusesADecisionHeldUnderADraftsID holds plan approval to what the plan drafted: a ledger may
// hold decisions another writer recorded under the IDs approval derives for the plan's drafts. Approval refuses,
// naming each decision, who recorded it, and its draft, so the member's approval never stands for an answer the
// plan did not hold, and nothing of the approval happens. An overturn changes a decision's status, so it never
// makes the decision the draft's, and the refusal does not offer one.
func TestAPlanApprovalRefusesADecisionHeldUnderADraftsID(t *testing.T) {
	t.Parallel()
	d, w, _ := aWorld(t)
	ctx := t.Context()
	source := retrySource("The first draft.")
	source.Write("plan.md", []byte(retryTwoDrafts))
	w.Planned(source)
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	plan := need.Approval.Plan
	held := map[string]string{"d1": "Q: Which provider first? A: Adyen", "d2": "Q: How many retries? A: Five"}
	for draft, statement := range held {
		heldUnderDraftID(t, d, questions.Decision{
			ID: derivedDecisionID(plan, need.Version, draft), Status: questions.DecisionDecided, Statement: statement, Rationale: "It is cheaper.",
			Scope: "plan:" + plan, Reversibility: questions.Reversible, Citations: []string{"docs/payments.md"}, Categories: []string{questions.CategoryProduct},
		}, authority.DelegateActor(memberOf(t, d), "dlg_1"))
	}

	_, err := d.Client.DecideApproval(ctx, need.Approval.ID, api.DecideApproval{Digest: need.Approval.SubjectDigest, Allow: true})
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Status != http.StatusConflict || refused.Detail.Code != api.CodeDecisionClash || !strings.Contains(refused.Detail.Message, "dlg_1") {
		t.Fatalf("approving over clashing decisions: got %v, want 409 %s naming who recorded them", err, api.CodeDecisionClash)
	}
	for draft, statement := range held {
		id := derivedDecisionID(plan, need.Version, draft)
		if !strings.Contains(refused.Detail.Message, id) || !strings.Contains(refused.Detail.Message, `"`+draft+`"`) {
			t.Errorf("the refusal does not name decision %s and its draft %q: %s", id, draft, refused.Detail.Message)
		}
		if decision, err := d.Client.Decision(ctx, id); err != nil || decision.Statement != statement {
			t.Errorf("the clashing decision is %+v: %v", decision, err)
		}
	}
	if strings.Contains(refused.Detail.Message, "overturn") {
		t.Errorf("the refusal offers an overturn, which leaves the clash: %s", refused.Detail.Message)
	}
	if approval, err := d.Client.Approval(ctx, need.Approval.ID); err != nil || approval.State != authority.ApprovalPending {
		t.Errorf("the refused approval is %+v: %v", approval, err)
	}
	orders, err := d.Client.WorkOrders(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, order := range orders {
		if order.Title == "Add the budget" && (order.State != workorders.ContractReview || order.ReviewedBy != "") {
			t.Errorf("the plan's work is %s, reviewed by %q, after a refused approval", order.State, order.ReviewedBy)
		}
	}
}

// TestAPlanApprovalKeepsADecisionThatSaysWhatItsDraftSays holds the other side: a decision under a draft's ID
// with the content approval would record is the same decision, which a retry finds, so approval goes on.
func TestAPlanApprovalKeepsADecisionThatSaysWhatItsDraftSays(t *testing.T) {
	t.Parallel()
	d, w, _ := aWorld(t)
	ctx := t.Context()
	w.Planned(retrySource("The first draft."))
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	plan := need.Approval.Plan
	id := derivedDecisionID(plan, need.Version, "d1")
	heldUnderDraftID(t, d, questions.Decision{
		ID: id, Status: questions.DecisionDecided, Statement: "Q: Which provider first? A: Stripe", Rationale: "It is the one with an account.",
		Scope: "plan:" + plan, Reversibility: questions.Reversible, Citations: []string{"plan_version:" + planning.ContractKey(plan, need.Version)},
		Categories: []string{questions.CategoryProduct}, DecidedBy: memberOf(t, d),
	}, ledger.Actor{Kind: ledger.ActorMember, ID: memberOf(t, d)})
	approved, err := d.Client.DecideApproval(ctx, need.Approval.ID, api.DecideApproval{Digest: need.Approval.SubjectDigest, Allow: true})
	if err != nil || approved.State != authority.ApprovalApproved {
		t.Fatalf("approving over the same decision: %+v %v", approved, err)
	}
	orders, err := d.Client.WorkOrders(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, order := range orders {
		if order.Title == "Add the budget" && order.State != workorders.Ready {
			t.Errorf("the plan's work is %s", order.State)
		}
	}
}
