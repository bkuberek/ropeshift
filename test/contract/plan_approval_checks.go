package contract

import (
	"slices"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// PlanApprovalChecks hold what the ledger promises about approving a plan in one step: a version covers
// every record that names the plan, each version asks a member to approve it, and an approval decides
// what the plan drafted and lets its contracts through review (FR-PLAN-9 to FR-PLAN-12).
var PlanApprovalChecks = []Check{
	{"PA1", "a plan version covers every record that names the plan, and a new one is made when any of them changes", checkPlanVersionCoversItsRecords},
	{"PA2", "each plan version raises one approval bound to its digest, and a newer version supersedes an open one", checkEachVersionRaisesAnApproval},
	{"PA3", "the store holds a plan approval with no lane, and no other kind", checkStoreHoldsPlanApprovalsOnly},
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
