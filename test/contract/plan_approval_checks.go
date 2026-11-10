package contract

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// PlanApprovalChecks hold what the ledger promises about approving a plan in one step: a version covers
// every record that names the plan, each version asks a member to approve it, and an approval decides
// what the plan drafted and lets its contracts through review (FR-PLAN-9 to FR-PLAN-12).
var PlanApprovalChecks = []Check{
	{"PA1", "a plan version covers every record that names the plan, and a new one is made when any of them changes", checkPlanVersionCoversItsRecords},
	{"PA2", "each plan version raises one approval bound to its digest, and a newer version supersedes an open one", checkEachVersionRaisesAnApproval},
	{"PA3", "the store holds a plan approval with no lane, and no other kind", checkStoreHoldsPlanApprovalsOnly},
	{"PA4", "approving a plan version decides each draft decision as the member's, once", checkApprovalDecidesTheDrafts},
	{"PA5", "only a member approves a plan, and nothing a refused caller tried changed", checkOnlyAMemberApprovesAPlan},
	{"PA6", "an approval names the digest the member saw, and a version a newer one replaced cannot be approved", checkApprovalHoldsTheDigest},
	{"PA7", "a newer version decides the drafts it changed, replacing the earlier answer, and leaves the others", checkLaterVersionReplacesAnswers},
	{"PA8", "denying a plan decides nothing and passes no review", checkDenyingAPlan},
	{"PA9", "approving a plan passes the review of the work its contracts cover, and the definition of ready still applies", checkApprovalPassesCoveredReviews},
	{"PA10", "a contract the approved version does not cover stays in review until its version is approved", checkUncoveredContractStaysInReview},
	{"PA11", "a contract whose content an approved version covers is passed by the tick", checkTickPassesCoveredContracts},
	{"PA12", "a planning lane's branch asks for approval before anyone accepts it, and records nothing until then", checkLaneBranchAsksBeforeItIsRecorded},
	{"PA13", "approving a lane's plan accepts its work order, and intake then records the commit, decides the drafts, and passes the review", checkApprovalAcceptsTheLanesPlan},
	{"PA14", "a plan accepted by hand first is approved as the version intake recorded", checkAcceptedFirstIsApprovedAfterwards},
	{"PA15", "an approval of a branch that intake reads differently is void, and the version asks again", checkChangedBranchVoidsTheApproval},
}

const approvalPlan = "retry"

// approvalSource is a plan with a spec and one contract under it.
type approvalSource struct {
	source *fake.WorkSource
}

func newApprovalSource() approvalSource {
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(planWith(approvalPlan, "Retry budget", nil, "Stripe")))
	source.Write("spec.md", []byte(DocumentText("spec", "api", "The API", approvalPlan, "The first draft.")))
	source.Write("budget.md", []byte(placedContract{ID: "budget", Plan: approvalPlan}.text()))
	return approvalSource{source: source}
}

func (r *Rig) planVersionsOf(s ports.Store, plan string) ([]planning.PlanVersion, error) {
	var out []planning.PlanVersion
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Planning().PlanVersions()
		for _, version := range all {
			if version.PlanID == plan {
				out = append(out, version)
			}
		}
		return err
	})
	return out, err
}

func (r *Rig) planApprovals(s ports.Store) ([]authority.Approval, error) {
	all, err := control.NewApprovals(s).List(r.Ctx, r.Scope, "")
	var out []authority.Approval
	for _, approval := range all {
		if approval.Kind == authority.KindPlan {
			out = append(out, approval)
		}
	}
	return out, err
}

func checkPlanVersionCoversItsRecords(r *Rig) error {
	s := r.Store()
	files := newApprovalSource()
	report, err := r.intake(s, files.source)
	if err != nil {
		return err
	}
	if report.PlanVersions != 1 || len(report.Problems) != 0 {
		return Violatedf("a plan, a spec, and a contract made %+v", report)
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	versions, err := r.planVersionsOf(s, plan)
	if err != nil || len(versions) != 1 {
		return Violatedf("versions %+v: %v", versions, err)
	}
	first := versions[0]
	refs := []string{}
	for _, entry := range first.Records {
		refs = append(refs, entry.Ref)
	}
	slices.Sort(refs)
	if want := []string{"contract:budget", "plan:" + approvalPlan, "spec:api"}; !slices.Equal(refs, want) {
		return Violatedf("version 1 covers %v, want %v", refs, want)
	}
	if first.Digest != planning.PlanDigest(first.Records) || first.FileDigest == first.Digest || first.FileDigest == "" {
		return Violatedf("version 1 has digest %s over records %+v, and its file %s", first.Digest, first.Records, first.FileDigest)
	}
	contract, held, err := r.contractTitled(s, "budget")
	if err != nil || !held {
		return Violatedf("the contract: %v", err)
	}
	for _, entry := range first.Records {
		if entry.Ref == "contract:budget" && (entry.Digest != contract.Digest || entry.Record != contract.ID || len(entry.Parts) < 2) {
			return Violatedf("the contract entry %+v is not the contract %+v", entry, contract)
		}
	}

	// A spec that changed moves the digest, and the plan's own file stays what it was.
	files.source.Write("spec.md", []byte(DocumentText("spec", "api", "The API", approvalPlan, "The second draft.")))
	if report, err = r.intake(s, files.source); err != nil {
		return err
	}
	if report.PlanVersions != 1 {
		return Violatedf("a changed spec made %+v", report)
	}
	// So does a contract that changed.
	files.source.Write("budget.md", []byte(placedContract{ID: "budget", Plan: approvalPlan, Orders: []orderIn{{ID: "budget-order"}, {ID: "budget-more"}}}.text()))
	if report, err = r.intake(s, files.source); err != nil {
		return err
	}
	if report.PlanVersions != 1 {
		return Violatedf("a changed contract made %+v", report)
	}
	if versions, err = r.planVersionsOf(s, plan); err != nil || len(versions) != 3 {
		return Violatedf("versions %+v: %v", versions, err)
	}
	if versions[1].FileDigest != first.FileDigest || versions[2].FileDigest != first.FileDigest {
		return Violatedf("a document or a contract changed the plan's file digest: %+v", versions)
	}
	if versions[0].Digest == versions[1].Digest || versions[1].Digest == versions[2].Digest || versions[0].Digest == versions[2].Digest {
		return Violatedf("three versions share a digest: %+v", versions)
	}
	if !slices.Equal(versions[2].Documents, first.Documents) || len(versions[2].Decisions) != 1 || versions[2].Decisions[0].Answer != "Stripe" {
		return Violatedf("a version made for a document keeps what the plan file drafted: %+v", versions[2])
	}

	// Nothing changed, so nothing is made and nothing is written.
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if report, err = r.intake(s, files.source); err != nil {
		return err
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if report.PlanVersions != 0 || after != before {
		return Violatedf("a poll that changed nothing made %+v and moved the revision from %d to %d", report, before, after)
	}
	return r.CheckPlanning(s)
}

func checkEachVersionRaisesAnApproval(r *Rig) error {
	s := r.Store()
	files := newApprovalSource()
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	pending, err := r.planApprovals(s)
	if err != nil || len(pending) != 1 {
		return Violatedf("the first version raised %+v: %v", pending, err)
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	first, err := r.planVersionsOf(s, plan)
	if err != nil {
		return err
	}
	a := pending[0]
	if a.State != authority.ApprovalPending || a.SubjectDigest != first[0].Digest || a.Plan != plan || a.PlanVersion != 1 ||
		a.Subject != planning.ContractKey(plan, 1) || a.Lane != "" || a.Generation != 0 || a.ExpiresAt != 0 || a.Summary == "" {
		return Violatedf("the request for version 1 is %+v", a)
	}
	if a.RequestedBy != "system:intake" {
		return Violatedf("a polled source's plan was requested by %q", a.RequestedBy)
	}
	files.source.Write("spec.md", []byte(DocumentText("spec", "api", "The API", approvalPlan, "The second draft.")))
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	all, err := r.planApprovals(s)
	if err != nil || len(all) != 2 {
		return Violatedf("two versions raised %+v: %v", all, err)
	}
	if all[0].ID != a.ID || all[0].State != authority.ApprovalSuperseded {
		return Violatedf("version 2 left version 1's request %+v", all[0])
	}
	versions, err := r.planVersionsOf(s, plan)
	if err != nil || len(versions) != 2 {
		return Violatedf("versions %+v: %v", versions, err)
	}
	if all[1].State != authority.ApprovalPending || all[1].SubjectDigest != versions[1].Digest || all[1].PlanVersion != 2 {
		return Violatedf("the request for version 2 is %+v", all[1])
	}
	// A request is a record of the log, and replays from it.
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	replayed, err := authority.ReplayApprovals(events)
	if err != nil || len(replayed) != 2 || replayed[all[0].ID].State != authority.ApprovalSuperseded || replayed[all[1].ID].PlanVersion != 2 {
		return Violatedf("the log replays to %+v: %v", replayed, err)
	}
	return nil
}

// checkStoreHoldsPlanApprovalsOnly writes past every use case: the table holds no lane for a plan and
// for no other kind, and a plan names its version only.
func checkStoreHoldsPlanApprovalsOnly(r *Rig) error {
	s := r.Store()
	insert := func(a authority.Approval) error {
		return s.Write(r.Ctx, r.Scope, "test.approval", func(tx ports.WriteTx) error {
			if _, err := tx.Approvals().Insert(a); err != nil {
				return err
			}
			event, err := authority.ApprovalEvent(a, ledger.Actor{Kind: ledger.ActorSystem, ID: "test"})
			if err != nil {
				return err
			}
			_, err = tx.Append(event)
			return err
		})
	}
	plan := authority.Approval{
		ID: "p1", Kind: authority.KindPlan, Subject: "x@1", SubjectDigest: "sha256:x", Summary: "A plan", RequestedBy: "system:intake",
		State: authority.ApprovalPending, Plan: "x", PlanVersion: 1, Detail: `{"title":"X"}`,
	}
	if err := insert(plan); err != nil {
		return Violatedf("the store refused a plan with no lane: %v", err)
	}
	read, err := control.NewApprovals(s).Get(r.Ctx, r.Scope, "p1")
	if err != nil || read.Plan != "x" || read.PlanVersion != 1 || read.Detail != plan.Detail || read.Lane != "" || read.WorkOrder != "" {
		return Violatedf("the plan reads back as %+v: %v", read, err)
	}
	call := aCall(gatedInput)
	tool := authority.Approval{
		ID: "t1", Kind: authority.KindToolCall, Subject: call.Tool, SubjectDigest: call.Digest, Input: call.Input, Summary: call.Summary,
		RequestedBy: "lane:l1", State: authority.ApprovalPending,
	}
	if err := insert(tool); err == nil {
		return Violatedf("the store took a tool call that names no lane")
	}
	tool.Lane, tool.WorkOrder, tool.Plan, tool.PlanVersion = "l1", "w1", "x", 1
	if err := insert(tool); err == nil {
		return Violatedf("the store took a tool call that names a plan")
	}
	// Only a member decides a plan, which the table holds as well as the code.
	open := plan
	open.ID = "p2"
	if err := insert(open); err != nil {
		return err
	}
	decided := open
	decided.State, decided.DecidedBy, decided.DecidedKind, decided.DecidedDigest = authority.ApprovalApproved, "orchestrator:w1", "orchestrator", open.SubjectDigest
	err = s.Write(r.Ctx, r.Scope, "test.approval", func(tx ports.WriteTx) error {
		_, err := tx.Approvals().Save(decided, authority.ApprovalPending)
		return err
	})
	if err == nil {
		return Violatedf("the store let an orchestrator approve a plan")
	}
	return nil
}

func (r *Rig) approve(s ports.Store, a authority.Approval, allow bool, actor ledger.Actor) (authority.Approval, error) {
	return control.NewApprovals(s).Decide(r.Ctx, r.Scope, authority.Decision{ID: a.ID, Digest: a.SubjectDigest, Allow: allow}, actor)
}

// theOpenPlanApproval reads the one request waiting, and theOpenPlanApprovalFor the one of a plan.
func (r *Rig) theOpenPlanApproval(s ports.Store) (authority.Approval, error) {
	return r.theOpenPlanApprovalFor(s, "")
}

func (r *Rig) theOpenPlanApprovalFor(s ports.Store, title string) (authority.Approval, error) {
	all, err := r.planApprovals(s)
	if err != nil {
		return authority.Approval{}, err
	}
	var open []authority.Approval
	for _, a := range all {
		var detail planning.PlanDetail
		if err := json.Unmarshal([]byte(a.Detail), &detail); err != nil {
			return authority.Approval{}, Violatedf("the detail of %s is %q: %v", a.ID, a.Detail, err)
		}
		if a.Open() && (title == "" || detail.Title == title) {
			open = append(open, a)
		}
	}
	if len(open) != 1 {
		return authority.Approval{}, Violatedf("%d plan approvals wait: %+v", len(open), all)
	}
	return open[0], nil
}

// planDecisions reads the decisions the plan approvals made, newest last.
func (r *Rig) planDecisions(s ports.Store, plan string) ([]questions.Decision, error) {
	var out []questions.Decision
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Questions().Decisions(ports.RecordFilter{Scope: "plan:" + plan})
		return err
	})
	return out, err
}

func (r *Rig) orderOfContract(s ports.Store, contract string) (workorders.WorkOrder, error) {
	return r.orderByTitle(s, "The work order "+contract+"-order")
}

func checkApprovalDecidesTheDrafts(r *Rig) error {
	s := r.Store()
	files := newApprovalSource()
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	pending, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	if decisions, err := r.planDecisions(s, plan); err != nil || len(decisions) != 0 {
		return Violatedf("a draft decided before anyone approved the plan: %+v: %v", decisions, err)
	}
	approved, err := r.approve(s, pending, true, theMember)
	if err != nil {
		return err
	}
	if approved.State != authority.ApprovalApproved || approved.DecidedBy != authority.Principal(theMember) || approved.DecidedDigest != pending.SubjectDigest {
		return Violatedf("the approval is %+v", approved)
	}
	decisions, err := r.planDecisions(s, plan)
	if err != nil || len(decisions) != 1 {
		return Violatedf("approval decided %+v: %v", decisions, err)
	}
	d := decisions[0]
	if d.Status != questions.DecisionDecided || d.DecidedBy != theMember.ID || d.QuestionID != "" || d.Scope != "plan:"+plan ||
		d.Reversibility != questions.Reversible || !slices.Equal(d.Categories, []string{questions.CategoryProduct}) ||
		!slices.Contains(d.Citations, "plan_version:"+planning.ContractKey(plan, 1)) || d.Rationale == "" {
		return Violatedf("the decision is %+v", d)
	}
	for _, want := range []string{"Which provider first?", "Stripe"} {
		if !strings.Contains(d.Statement, want) {
			return Violatedf("the decision states %q, and not %q", d.Statement, want)
		}
	}
	// A retry decides nothing twice.
	again, err := r.approve(s, pending, true, theMember)
	if err != nil || again.State != authority.ApprovalApproved {
		return Violatedf("the retry: %+v: %v", again, err)
	}
	if decisions, err = r.planDecisions(s, plan); err != nil || len(decisions) != 1 {
		return Violatedf("a retry decided again: %+v: %v", decisions, err)
	}
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	if book, err := questions.Replay(events); err != nil || len(book.Decisions) != 1 {
		return Violatedf("the log replays to %+v: %v", book.Decisions, err)
	}
	return nil
}

func checkOnlyAMemberApprovesAPlan(r *Rig) error {
	s := r.Store()
	if _, err := r.intake(s, newApprovalSource().source); err != nil {
		return err
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	pending, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorOrchestrator, ID: "worker-1"}, {Kind: ledger.ActorLane, ID: "lane-1"},
		{Kind: ledger.ActorWorker, ID: "worker-1"}, {Kind: ledger.ActorSystem, ID: "intake"},
	} {
		if _, err := r.approve(s, pending, true, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s approving a plan: %v", actor.Kind, err)
		}
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a refused caller moved the ledger from %d to %d: %v", before, after, err)
	}
	if decisions, err := r.planDecisions(s, plan); err != nil || len(decisions) != 0 {
		return Violatedf("a refused caller decided %+v: %v", decisions, err)
	}
	order, err := r.orderOfContract(s, "budget")
	if err != nil || order.State != workorders.ContractReview || order.ReviewedBy != "" {
		return Violatedf("a refused caller moved the work order %+v: %v", order, err)
	}
	return nil
}

func checkApprovalHoldsTheDigest(r *Rig) error {
	s := r.Store()
	files := newApprovalSource()
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	first, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	stale := authority.Decision{ID: first.ID, Digest: "sha256:an-older-plan", Allow: true}
	if _, err := control.NewApprovals(s).Decide(r.Ctx, r.Scope, stale, theMember); !errors.Is(err, authority.ErrStaleDigest) {
		return Violatedf("a digest the member never saw: %v", err)
	}
	files.source.Write("spec.md", []byte(DocumentText("spec", "api", "The API", approvalPlan, "The second draft.")))
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	if _, err := r.approve(s, first, true, theMember); !errors.Is(err, authority.ErrApprovalClosed) {
		return Violatedf("a member approved a version a newer one replaced: %v", err)
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	if decisions, err := r.planDecisions(s, plan); err != nil || len(decisions) != 0 {
		return Violatedf("a refused approval decided %+v: %v", decisions, err)
	}
	current, err := r.theOpenPlanApproval(s)
	if err != nil || current.PlanVersion != 2 {
		return Violatedf("the open request is %+v: %v", current, err)
	}
	return nil
}

func checkLaterVersionReplacesAnswers(r *Rig) error {
	s := r.Store()
	files := newApprovalSource()
	two := `+++
kind = "plan"
id = "` + approvalPlan + `"
title = "Retry budget"

[[decision]]
id = "d1"
question = "Which provider first?"
answer = "Stripe"
rationale = "It is the one with an account."
blocking = true

[[decision]]
id = "d2"
question = "How many retries?"
answer = "Three"
rationale = "It is what the spec says."
+++

How.
`
	files.source.Write("plan.md", []byte(two))
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	first, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	if _, err := r.approve(s, first, true, theMember); err != nil {
		return err
	}
	if decisions, err := r.planDecisions(s, plan); err != nil || len(decisions) != 2 {
		return Violatedf("version 1 decided %+v: %v", decisions, err)
	}
	// Version 2 changes one answer. The other draft is the same, so it is not decided again.
	files.source.Write("plan.md", []byte(strings.Replace(two, `answer = "Stripe"`, `answer = "Adyen"`, 1)))
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	second, err := r.theOpenPlanApproval(s)
	if err != nil || second.PlanVersion != 2 {
		return Violatedf("the open request is %+v: %v", second, err)
	}
	if _, err := r.approve(s, second, true, theMember); err != nil {
		return err
	}
	var inForce []questions.Decision
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Questions().Decisions(ports.RecordFilter{Scope: "plan:" + plan})
		for _, d := range all {
			if d.Status.InForce() {
				inForce = append(inForce, d)
			}
		}
		return err
	})
	if err != nil || len(inForce) != 2 {
		return Violatedf("in force after version 2: %+v: %v", inForce, err)
	}
	stated := ""
	for _, d := range inForce {
		stated += d.Statement + "|"
	}
	if !strings.Contains(stated, "Adyen") || strings.Contains(stated, "Stripe") || !strings.Contains(stated, "Three") {
		return Violatedf("the answers in force are %q", stated)
	}
	for _, d := range inForce {
		if strings.Contains(d.Statement, "Adyen") && d.Supersedes == "" {
			return Violatedf("the new answer replaced nothing: %+v", d)
		}
	}
	return nil
}

func checkDenyingAPlan(r *Rig) error {
	s := r.Store()
	if _, err := r.intake(s, newApprovalSource().source); err != nil {
		return err
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	pending, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	denied, err := r.approve(s, pending, false, theMember)
	if err != nil || denied.State != authority.ApprovalDenied {
		return Violatedf("denied: %+v: %v", denied, err)
	}
	if decisions, err := r.planDecisions(s, plan); err != nil || len(decisions) != 0 {
		return Violatedf("a denied plan decided %+v: %v", decisions, err)
	}
	if _, err := control.NewReviews(s).Promote(r.Ctx, r.Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake-tick"}); err != nil {
		return err
	}
	order, err := r.orderOfContract(s, "budget")
	if err != nil || order.State != workorders.ContractReview || order.ReviewedBy != "" {
		return Violatedf("a denied plan let the work order through: %+v: %v", order, err)
	}
	return nil
}

func checkApprovalPassesCoveredReviews(r *Rig) error {
	s := r.Store()
	files := newApprovalSource()
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	order, err := r.orderOfContract(s, "budget")
	if err != nil || order.State != workorders.ContractReview || order.ReviewedBy != "" {
		return Violatedf("before approval %+v: %v", order, err)
	}
	pending, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return err
	}
	if order, err = r.orderOfContract(s, "budget"); err != nil {
		return err
	}
	if order.State != workorders.Ready || order.ReviewedBy != theMember.ID {
		return Violatedf("after approval the work order is %s, reviewed by %q", order.State, order.ReviewedBy)
	}
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	cited := false
	for _, e := range events {
		if e.Type == workorders.EventReviewPassed && e.Subject.ID == order.ID && strings.Contains(string(e.Data), pending.ID) {
			cited = true
		}
	}
	if !cited {
		return Violatedf("no review event cites the approval %s", pending.ID)
	}
	if book, err := workorders.Replay(events); err != nil || book[order.ID].ReviewedBy != theMember.ID {
		return Violatedf("the log replays the review to %+v: %v", book[order.ID], err)
	}

	// A question the review collected still holds the work back, with the review kept.
	files.source.Write("held-plan.md", []byte(planWith("held", "Held plan", nil, "Stripe")))
	files.source.Write("held.md", []byte(placedContract{ID: "held", Plan: "held"}.text()))
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	held, err := r.orderOfContract(s, "held")
	if err != nil {
		return err
	}
	if _, err := r.ask(s, "Is this the right budget?", "lane-a", func(a *questions.Ask) {
		a.Blocking, a.SubjectKind, a.SubjectID = true, workorders.SubjectKind, held.ID
	}); err != nil {
		return err
	}
	pending, err = r.theOpenPlanApprovalFor(s, "Held plan")
	if err != nil {
		return err
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return err
	}
	if held, err = r.orderOfContract(s, "held"); err != nil {
		return err
	}
	if held.State != workorders.ContractReview || held.ReviewedBy != theMember.ID {
		return Violatedf("a work order with an open blocking question is %s, reviewed by %q", held.State, held.ReviewedBy)
	}
	return nil
}

func checkUncoveredContractStaysInReview(r *Rig) error {
	s := r.Store()
	files := newApprovalSource()
	files.source.Write("alone.md", []byte(placedContract{ID: "alone"}.text()))
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	pending, err := r.theOpenPlanApprovalFor(s, "Retry budget")
	if err != nil {
		return err
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return err
	}
	if alone, err := r.orderOfContract(s, "alone"); err != nil || alone.State != workorders.ContractReview || alone.ReviewedBy != "" {
		return Violatedf("a contract with no plan was passed by another plan's approval: %+v: %v", alone, err)
	}
	// A change to the contract is a version the approval did not cover.
	files.source.Write("budget.md", []byte(placedContract{ID: "budget", Plan: approvalPlan, Orders: []orderIn{{ID: "budget-order"}, {ID: "budget-more"}}}.text()))
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	changed, err := r.orderOfContract(s, "budget")
	if err != nil || changed.State != workorders.ContractReview || changed.ReviewedBy != "" {
		return Violatedf("a changed contract is %+v: %v", changed, err)
	}
	if _, err := control.NewReviews(s).Promote(r.Ctx, r.Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake-tick"}); err != nil {
		return err
	}
	if changed, err = r.orderOfContract(s, "budget"); err != nil || changed.State != workorders.ContractReview || changed.ReviewedBy != "" {
		return Violatedf("the tick passed a version no approval covers: %+v: %v", changed, err)
	}
	second, err := r.theOpenPlanApproval(s)
	if err != nil || second.PlanVersion != 2 {
		return Violatedf("the open request is %+v: %v", second, err)
	}
	if _, err := r.approve(s, second, true, theMember); err != nil {
		return err
	}
	if changed, err = r.orderOfContract(s, "budget"); err != nil || changed.State != workorders.Ready || changed.ReviewedBy != theMember.ID {
		return Violatedf("after version 2 is approved: %+v: %v", changed, err)
	}
	return nil
}

func checkTickPassesCoveredContracts(r *Rig) error {
	s := r.Store()
	files := newApprovalSource()
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	first, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	if _, err := r.approve(s, first, true, theMember); err != nil {
		return err
	}
	// The contract changes and then returns to what the member approved, so its digest is one an approved
	// version covers, and the work goes back through review without anyone looking at it again.
	original := placedContract{ID: "budget", Plan: approvalPlan}.text()
	files.source.Write("budget.md", []byte(placedContract{ID: "budget", Plan: approvalPlan, Orders: []orderIn{{ID: "budget-order"}, {ID: "budget-more"}}}.text()))
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	if order, err := r.orderOfContract(s, "budget"); err != nil || order.State != workorders.ContractReview || order.ReviewedBy != "" {
		return Violatedf("after the change %+v: %v", order, err)
	}
	files.source.Write("budget.md", []byte(original))
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	if _, err := r.NewOps(s).Promote(r.Ctx, r.Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake-tick"}); err != nil {
		return err
	}
	order, err := r.orderOfContract(s, "budget")
	if err != nil || order.State != workorders.Ready || order.ReviewedBy != theMember.ID {
		return Violatedf("after the contract returned to what was approved: %+v: %v", order, err)
	}
	return nil
}

// laneFiles are what a planning lane left on its branch: a plan that drafts an answer, a spec, and a
// contract.
func laneFiles(spec string) map[string]string {
	return map[string]string{
		"plan.md":   planWith(approvalPlan, "Retry budget", nil, "Stripe"),
		"spec.md":   DocumentText("spec", "api", "The API", approvalPlan, spec),
		"budget.md": placedContract{ID: "budget", Plan: approvalPlan}.text(),
	}
}

// aLanePlanAsksForApproval takes a plan lane's branch in a tick and returns the one request it raised.
func (r *Rig) aLanePlanAsksForApproval(s ports.Store, files map[string]string) (string, authority.Approval, error) {
	id, err := r.planLane(s)
	if err != nil {
		return "", authority.Approval{}, err
	}
	var asked []string
	taken, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(files), &asked))
	if err != nil {
		return "", authority.Approval{}, err
	}
	if len(taken) != 0 {
		return "", authority.Approval{}, Violatedf("a plan nobody accepted was taken in: %v", taken)
	}
	pending, err := r.theOpenPlanApproval(s)
	return id, pending, err
}

func checkLaneBranchAsksBeforeItIsRecorded(r *Rig) error {
	s := r.Store()
	id, pending, err := r.aLanePlanAsksForApproval(s, laneFiles("The first draft."))
	if err != nil {
		return err
	}
	var detail planning.PlanDetail
	if err := json.Unmarshal([]byte(pending.Detail), &detail); err != nil {
		return err
	}
	if pending.WorkOrder != id || pending.Plan != "" || pending.PlanVersion != 1 || detail.Ref != approvalPlan || detail.Source != acceptedHead ||
		!strings.HasPrefix(pending.RequestedBy, "lane:") || detail.Title != "Retry budget" || detail.Decisions != 1 ||
		detail.Difference.Base != 0 || len(detail.Difference.Added) != 3 || pending.SubjectDigest == "" {
		return Violatedf("the request is %+v with detail %+v", pending, detail)
	}
	// Nothing the lane wrote is a record, and the work order still waits for acceptance.
	if _, held, err := r.contractTitled(s, "budget"); err != nil || held {
		return Violatedf("the branch left its contract in the ledger (%v): %v", held, err)
	}
	if versions, err := r.planVersionsOf(s, ""); err != nil || len(versions) != 0 {
		return Violatedf("the branch left plan versions %+v: %v", versions, err)
	}
	if row, err := r.WorkOrder(s, id); err != nil || row.State != workorders.Acceptance {
		return Violatedf("the plan work order is %+v: %v", row, err)
	}
	// The same commit asks once, and a tick that finds it again writes nothing.
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	var asked []string
	if _, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(laneFiles("The first draft.")), &asked)); err != nil {
		return err
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a second tick moved the revision from %d to %d: %v", before, after, err)
	}
	return nil
}

func checkApprovalAcceptsTheLanesPlan(r *Rig) error {
	s := r.Store()
	files := laneFiles("The first draft.")
	id, pending, err := r.aLanePlanAsksForApproval(s, files)
	if err != nil {
		return err
	}
	approved, err := r.approve(s, pending, true, theMember)
	if err != nil || approved.State != authority.ApprovalApproved {
		return Violatedf("approval: %+v: %v", approved, err)
	}
	if row, err := r.WorkOrder(s, id); err != nil || row.State != workorders.Done {
		return Violatedf("after approval the plan work order is %+v: %v", row, err)
	}
	// Intake has not read the commit yet, so the plan has no records, and the approval decided nothing.
	if _, held, err := r.contractTitled(s, "budget"); err != nil || held {
		return Violatedf("approval recorded the contract of the branch (%v): %v", held, err)
	}
	var asked []string
	taken, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(files), &asked))
	if err != nil || !slices.Equal(taken, []string{id}) {
		return Violatedf("taken %v: %v", taken, err)
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	versions, err := r.planVersionsOf(s, plan)
	if err != nil || len(versions) != 1 || versions[0].Digest != pending.SubjectDigest || versions[0].WorkOrder != id {
		return Violatedf("versions %+v for the digest %s: %v", versions, pending.SubjectDigest, err)
	}
	all, err := r.planApprovals(s)
	if err != nil || len(all) != 1 || all[0].State != authority.ApprovalApproved || all[0].Plan != plan || all[0].PlanVersion != 1 {
		return Violatedf("the requests are %+v: %v", all, err)
	}
	decisions, err := r.planDecisions(s, plan)
	if err != nil || len(decisions) != 1 || decisions[0].DecidedBy != theMember.ID || !strings.Contains(decisions[0].Statement, "Stripe") {
		return Violatedf("decisions %+v: %v", decisions, err)
	}
	order, err := r.orderOfContract(s, "budget")
	if err != nil || order.State != workorders.Ready || order.ReviewedBy != theMember.ID {
		return Violatedf("the work of the contract is %+v: %v", order, err)
	}
	return r.CheckProjection(s)
}

func checkAcceptedFirstIsApprovedAfterwards(r *Rig) error {
	s := r.Store()
	files := laneFiles("The first draft.")
	id, pending, err := r.aLanePlanAsksForApproval(s, files)
	if err != nil {
		return err
	}
	if _, err := r.NewOps(s).Accept(r.Ctx, r.Scope, id, r.member()); err != nil {
		return err
	}
	var asked []string
	if _, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(files), &asked)); err != nil {
		return err
	}
	all, err := r.planApprovals(s)
	if err != nil || len(all) != 2 {
		return Violatedf("the requests are %+v: %v", all, err)
	}
	if all[0].ID != pending.ID || all[0].State != authority.ApprovalSuperseded {
		return Violatedf("the request for the branch was not replaced by the version intake made: %+v", all[0])
	}
	recorded := all[1]
	if recorded.State != authority.ApprovalPending || recorded.Plan == "" || recorded.PlanVersion != 1 || !strings.HasPrefix(recorded.RequestedBy, "lane:") ||
		recorded.WorkOrder != id || recorded.SubjectDigest != pending.SubjectDigest {
		return Violatedf("the request for the recorded version is %+v", recorded)
	}
	if _, err := r.approve(s, recorded, true, theMember); err != nil {
		return err
	}
	if order, err := r.orderOfContract(s, "budget"); err != nil || order.State != workorders.Ready {
		return Violatedf("the work of the contract is %+v: %v", order, err)
	}
	return nil
}

func checkChangedBranchVoidsTheApproval(r *Rig) error {
	s := r.Store()
	id, pending, err := r.aLanePlanAsksForApproval(s, laneFiles("The first draft."))
	if err != nil {
		return err
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return err
	}
	// What intake reads at the approved commit is not what the member approved.
	var asked []string
	taken, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(laneFiles("A different draft.")), &asked))
	if err != nil || !slices.Equal(taken, []string{id}) {
		return Violatedf("taken %v: %v", taken, err)
	}
	all, err := r.planApprovals(s)
	if err != nil || len(all) != 2 || all[0].State != authority.ApprovalVoid || all[1].State != authority.ApprovalPending {
		return Violatedf("the requests are %+v: %v", all, err)
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	if decisions, err := r.planDecisions(s, plan); err != nil || len(decisions) != 0 {
		return Violatedf("an approval of other files decided %+v: %v", decisions, err)
	}
	if order, err := r.orderOfContract(s, "budget"); err != nil || order.State != workorders.ContractReview || order.ReviewedBy != "" {
		return Violatedf("the work of the contract is %+v: %v", order, err)
	}
	return nil
}
