package contract

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// PlanMergeChecks are what the ledger promises about merging a planning branch (ADR-0018, ADR-0020,
// ADR-0023): no review judges it, a member's plan approval bound to its commit is the only approval of its
// merge, ropeshift merges it through the same integration step as any accepted work, and a branch that moved
// after the approval needs a new one.
var PlanMergeChecks = []Check{
	{"PM1", "at review_approves, an approved plan's branch waits in integrating until intake records the plan, and then its lane's worker merges the approved commit, with the plan version on record", checkApprovedPlanMerges},
	{"PM2", "through a pull request, a planning lane's end opens the pull request, and at person_approves the plan approval merges it at the approved head with no merge request of its own", checkApprovedPlanMergesItsPullRequest},
	{"PM3", "at person_merges with review target branch, approving a plan ends the planning and merges nothing", checkPersonMergesTheApprovedBranch},
	{"PM4", "at person_merges through a pull request, an approved plan waits in integrating for the person's merge, which is recorded with the plan approval", checkPersonMergesTheApprovedPullRequest},
	{"PM5", "a pull request whose head moved after the plan's approval goes back to acceptance at the new head, and only the new head's own plan approval merges it", checkMovedPlanningPullRequest},
	{"PM6", "with review target branch, a merge refused because the planning branch moved goes back to acceptance at the new head, which asks for its own approval", checkMovedPlanningBranch},
	{"PM7", "a conflict sends the planning work back to rework on the same branch, with the conflict in its brief", checkPlanningConflict},
	{"PM8", "approving an adopt session's plan merges the adoption branch the same way", checkAdoptionBranchMerges},
	{"PM9", "a planning branch accepted by hand waits in integrating for its plan approval, and a member's acceptance never approves its merge", checkPlanAcceptedByHandWaits},
}

// planMergeRig is a registered worker that ran a planning lane to completion in a repository under a review
// target and a merge authority level, and whose branch holds a plan, a spec, and a contract.
type planMergeRig struct {
	*forgeRig
	files map[string]string
}

func newPlanMergeRig(r *Rig, target ledger.ReviewTarget, level ledger.MergeAuthority, adopt bool) (*planMergeRig, error) {
	f, err := newForgeRigOf(r, target, func(repository string) control.LedgerWork {
		return control.LedgerWork{
			Order: workorders.WorkOrder{
				ID: r.ID("plan"), Kind: workorders.KindPlan, Title: "Plan the retry budget", RepositoryID: repository, MaxAttempts: 2, Adopt: adopt,
			},
			Statement: "Write the plan for a retry budget.",
			Criteria:  []planning.Criterion{{ID: "c1", Text: "The plan is on the branch"}},
		}
	}, LaneCheck("c1", true))
	if err != nil {
		return nil, err
	}
	if _, err := r.change(f.store, f.repository, control.RepositoryChange{MergeAuthority: &level}, r.Actor); err != nil {
		return nil, err
	}
	return &planMergeRig{forgeRig: f, files: laneFiles("The first draft.")}, nil
}

// tick runs intake over the planning branch, as its tick does: it asks about a branch nobody accepted, and
// records the plan of one a member accepted.
func (p *planMergeRig) tick() ([]string, error) {
	var asked []string
	return control.NewIntake(p.store).TakeInPlans(p.r.Ctx, p.r.Scope, openFrom(acceptedSource(p.files), &asked))
}

// asked is the one plan approval the branch asks for.
func (p *planMergeRig) asked() (authority.Approval, error) {
	if _, err := p.tick(); err != nil {
		return authority.Approval{}, err
	}
	return p.r.theOpenPlanApproval(p.store)
}

// approved asks for the plan's approval and approves it as the member.
func (p *planMergeRig) approved() (authority.Approval, error) {
	pending, err := p.asked()
	if err != nil {
		return authority.Approval{}, err
	}
	return p.r.approve(p.store, pending, true, theMember)
}

func (p *planMergeRig) order() (workorders.WorkOrder, error) {
	return p.r.WorkOrder(p.store, p.grant.WorkOrder)
}

// stands says the planning work is in a state.
func (p *planMergeRig) stands(state workorders.State) error {
	order, err := p.order()
	if err != nil || order.State != state {
		return Violatedf("the planning work is %+v, want %s: %v", order, state, err)
	}
	return nil
}

func (p *planMergeRig) integrations() ([]ledger.IntegrationRecord, error) {
	view, err := p.standing()
	return view.Integrations, err
}

// approvedBy says the one approval on record is the member's plan approval of commit at the level, for the
// plan's version.
func (p *planMergeRig) approvedBy(commit string, level ledger.MergeAuthority, version int64) (ledger.IntegrationRecord, error) {
	records, err := p.integrations()
	if err != nil {
		return ledger.IntegrationRecord{}, err
	}
	plan, err := p.r.planID(p.store, "Retry budget")
	if err != nil {
		return ledger.IntegrationRecord{}, err
	}
	for _, record := range records {
		if record.ApprovedCommit != commit {
			continue
		}
		if record.ApprovedByKind != ledger.ApprovedByMember || record.ApprovedByID != theMember.ID || record.MergeAuthority != level ||
			record.Plan != plan || record.PlanVersion != version || record.WorkOrder != p.grant.WorkOrder {
			return record, Violatedf("the approval of %s reads %+v, want the member's approval of plan %s version %d at %s", commit, record, plan, version, level)
		}
		return record, nil
	}
	return ledger.IntegrationRecord{}, Violatedf("no approval of %s among %+v", commit, records)
}

// noMergeRequest says no merge approval request was ever raised for the planning work: its plan approval is
// the request.
func (p *planMergeRig) noMergeRequest() error {
	rows, err := p.mergeRequests()
	if err != nil || len(rows) != 0 {
		return Violatedf("merge requests %+v of a planning branch: %v", rows, err)
	}
	return nil
}

// integrationEvents reads the integration events about the planning work, of a type.
func (p *planMergeRig) integrationEvents(eventType string) ([]ledger.Event, error) {
	var out []ledger.Event
	err := p.store.Read(p.r.Ctx, p.r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectIntegration, ID: p.grant.WorkOrder})
		for _, event := range events {
			if event.Type == eventType {
				out = append(out, event)
			}
		}
		return err
	})
	return out, err
}

// landed reports the merge as merged at a commit, and checks it ended the planning work with the merge on
// the approval of commit.
func (p *planMergeRig) landed(request ledger.DesiredForge, merged string) error {
	if err := p.mergeCame(request, ledger.MergeResult{Outcome: ledger.MergeMerged, MergeCommit: merged, MergedAt: "2026-11-12T10:00:00Z"}); err != nil {
		return err
	}
	if err := p.stands(workorders.Done); err != nil {
		return err
	}
	records, err := p.integrations()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.ApprovedCommit == request.Commit && record.MergedCommit == merged && record.Merged() && record.Plan != "" {
			return nil
		}
	}
	return Violatedf("the merge of %s is not on record: %+v", request.Commit, records)
}

func checkApprovedPlanMerges(r *Rig) error {
	p, err := newPlanMergeRig(r, ledger.ReviewOnBranch, ledger.ReviewApproves, false)
	if err != nil {
		return err
	}
	if _, err := p.approved(); err != nil {
		return err
	}
	// The plan has no records yet, so nothing merges: the records exist before any merge.
	if err := p.stands(workorders.Integrating); err != nil {
		return err
	}
	if err := p.noMerge(); err != nil {
		return err
	}
	if records, err := p.integrations(); err != nil || len(records) != 0 {
		return Violatedf("an approval on record before intake read the plan: %+v, %v", records, err)
	}
	if view, err := p.standing(); err != nil || !strings.Contains(view.Why, "plan approval") {
		return Violatedf("the merge waits on %q: %v", view.Why, err)
	}
	if _, err := p.tick(); err != nil {
		return err
	}
	if _, err := p.approvedBy(p.head, ledger.ReviewApproves, 1); err != nil {
		return err
	}
	merge, err := p.oneMerge()
	if err != nil {
		return err
	}
	if merge.WorkOrder != p.grant.WorkOrder || merge.Commit != p.head || merge.Branch != branchName || merge.Base != "main" ||
		merge.Method != ledger.MergeCommit || merge.Number != 0 || merge.Repository != p.repository {
		return Violatedf("the worker is asked to merge %+v", merge)
	}
	if err := p.noMergeRequest(); err != nil {
		return err
	}
	if err := p.landed(merge, "merged-plan"); err != nil {
		return err
	}
	approved, err := p.integrationEvents(ledger.EventIntegrationApproved)
	if err != nil || len(approved) != 1 || approved[0].Actor.ID != theMember.ID || !strings.Contains(string(approved[0].Data), `"plan_version":1`) {
		return Violatedf("the approval events %+v: %v", approved, err)
	}
	merged, err := p.integrationEvents(ledger.EventIntegrationMerged)
	if err != nil || len(merged) != 1 || !strings.Contains(string(merged[0].Data), `"merged_commit":"merged-plan"`) || !strings.Contains(string(merged[0].Data), `"plan_id"`) {
		return Violatedf("the merge events %+v: %v", merged, err)
	}
	// The approved plan's work goes on, as approving a plan always made it.
	if order, err := r.orderOfContract(p.store, "budget"); err != nil || order.State != workorders.Ready {
		return Violatedf("the plan's work is %+v: %v", order, err)
	}
	return r.CheckProjection(p.store)
}

func checkApprovedPlanMergesItsPullRequest(r *Rig) error {
	p, err := newPlanMergeRig(r, ledger.ReviewOnPullRequest, ledger.PersonApproves, false)
	if err != nil {
		return err
	}
	publish, err := p.publish()
	if err != nil {
		return err
	}
	if publish.Kind != ledger.ForgePublish || publish.WorkOrder != p.grant.WorkOrder || publish.Commit != p.head || publish.Branch != branchName ||
		publish.Base != "main" || publish.Binding != "github.com/acme/widgets" || !strings.Contains(publish.Body, p.grant.WorkOrder) ||
		!strings.Contains(publish.Body, "plan approval") {
		return Violatedf("the planning lane's end asks %+v", publish)
	}
	if err := p.report(p.opened(1, p.head)); err != nil {
		return err
	}
	if _, err := p.approved(); err != nil {
		return err
	}
	if err := p.noMerge(); err != nil {
		return err
	}
	if _, err := p.tick(); err != nil {
		return err
	}
	if _, err := p.approvedBy(p.head, ledger.PersonApproves, 1); err != nil {
		return err
	}
	merge, err := p.oneMerge()
	if err != nil {
		return err
	}
	if merge.Number != 7 || merge.Commit != p.head || merge.Binding != "github.com/acme/widgets" || merge.Method != ledger.MergeCommit {
		return Violatedf("the worker is asked to merge %+v", merge)
	}
	if err := p.noMergeRequest(); err != nil {
		return err
	}
	return p.landed(merge, "merged-pr")
}

// checkPersonMergesTheApprovedBranch: the approval ends the planning, and the person merges the branch when
// they choose.
func checkPersonMergesTheApprovedBranch(r *Rig) error {
	p, err := newPlanMergeRig(r, ledger.ReviewOnBranch, ledger.PersonMerges, false)
	if err != nil {
		return err
	}
	if _, err := p.approved(); err != nil {
		return err
	}
	if err := p.stands(workorders.Done); err != nil {
		return err
	}
	if _, err := p.tick(); err != nil {
		return err
	}
	if err := p.noMerge(); err != nil {
		return err
	}
	if records, err := p.integrations(); err != nil || len(records) != 0 {
		return Violatedf("a branch at person_merges has approvals on record: %+v, %v", records, err)
	}
	if order, err := r.orderOfContract(p.store, "budget"); err != nil || order.State != workorders.Ready {
		return Violatedf("the plan's work is %+v: %v", order, err)
	}
	return nil
}

func checkPersonMergesTheApprovedPullRequest(r *Rig) error {
	p, err := newPlanMergeRig(r, ledger.ReviewOnPullRequest, ledger.PersonMerges, false)
	if err != nil {
		return err
	}
	if err := p.report(p.opened(1, p.head)); err != nil {
		return err
	}
	if _, err := p.approved(); err != nil {
		return err
	}
	if _, err := p.tick(); err != nil {
		return err
	}
	if err := p.stands(workorders.Integrating); err != nil {
		return err
	}
	if err := p.noMerge(); err != nil {
		return err
	}
	if err := p.noMergeRequest(); err != nil {
		return err
	}
	if _, err := p.approvedBy(p.head, ledger.PersonMerges, 1); err != nil {
		return err
	}
	merged := p.pullRequestAt(p.head)
	merged.State, merged.MergeCommit, merged.MergedAt = ledger.PullRequestMerged, "persons-merge", "2026-11-12T11:00:00Z"
	if err := p.read(merged); err != nil {
		return err
	}
	if err := p.stands(workorders.Done); err != nil {
		return err
	}
	record, err := p.approvedBy(p.head, ledger.PersonMerges, 1)
	if err != nil || record.MergedCommit != "persons-merge" || record.PullRequest != 7 {
		return Violatedf("the person's merge is recorded as %+v: %v", record, err)
	}
	return p.noMerge()
}

// movedPlan is a commit the planning branch moved to after its approval, whose plan changed.
func (p *planMergeRig) movedPlan() string {
	p.files = laneFiles("A second draft.")
	return p.r.ID("moved")
}

// askedAgainAt says the planning work went back to acceptance at the new head, which asks for its own
// approval, while the approval of the first version stands as a plan approval.
func (p *planMergeRig) askedAgainAt(head string) (authority.Approval, error) {
	if err := p.stands(workorders.Acceptance); err != nil {
		return authority.Approval{}, err
	}
	view, err := p.standing()
	if err != nil {
		return authority.Approval{}, err
	}
	if branches := branchesIn(view.Artifacts); len(branches) == 0 || branches[len(branches)-1].Digest != head {
		return authority.Approval{}, Violatedf("the planning work's branch reads %+v, want its head at %s", branches, head)
	}
	if rows, err := p.mergeRequests(); err != nil || slices.ContainsFunc(rows, authority.Approval.Open) {
		return authority.Approval{}, Violatedf("merge requests %+v still stand: %v", rows, err)
	}
	pending, err := p.asked()
	if err != nil {
		return authority.Approval{}, err
	}
	if planDetailOf(pending).Source != head || pending.PlanVersion != 2 || pending.WorkOrder != p.grant.WorkOrder {
		return authority.Approval{}, Violatedf("the new head asks %+v", pending)
	}
	all, err := p.r.planApprovals(p.store)
	if err != nil || len(all) != 2 || all[0].State != authority.ApprovalApproved || all[0].PlanVersion != 1 {
		return authority.Approval{}, Violatedf("the plan approvals read %+v: %v", all, err)
	}
	return pending, nil
}

func branchesIn(artifacts []ledger.Artifact) []ledger.Artifact {
	var out []ledger.Artifact
	for _, artifact := range artifacts {
		if artifact.Kind == ledger.ArtifactBranch {
			out = append(out, artifact)
		}
	}
	return out
}

func planDetailOf(a authority.Approval) planning.PlanDetail {
	var detail planning.PlanDetail
	_ = json.Unmarshal([]byte(a.Detail), &detail)
	return detail
}

func checkMovedPlanningPullRequest(r *Rig) error {
	p, err := newPlanMergeRig(r, ledger.ReviewOnPullRequest, ledger.ReviewApproves, false)
	if err != nil {
		return err
	}
	if err := p.report(p.opened(1, p.head)); err != nil {
		return err
	}
	// A check the base requires still runs, so the approved head waits.
	if err := p.read(p.pullRequestAt(p.head, requiredCheck("CI result", ledger.CheckPending))); err != nil {
		return err
	}
	if _, err := p.approved(); err != nil {
		return err
	}
	if _, err := p.tick(); err != nil {
		return err
	}
	if err := p.noMerge(); err != nil {
		return err
	}
	// Someone pushes a new plan to the pull request's branch.
	moved := p.movedPlan()
	if err := p.read(p.pullRequestAt(moved)); err != nil {
		return err
	}
	if err := p.noMerge(); err != nil {
		return err
	}
	pending, err := p.askedAgainAt(moved)
	if err != nil {
		return err
	}
	if _, err := p.r.approve(p.store, pending, true, theMember); err != nil {
		return err
	}
	if _, err := p.tick(); err != nil {
		return err
	}
	merge, err := p.oneMerge()
	if err != nil || merge.Commit != moved {
		return Violatedf("the worker is asked to merge %+v, want the new head %s: %v", merge, moved, err)
	}
	if _, err := p.approvedBy(moved, ledger.ReviewApproves, 2); err != nil {
		return err
	}
	old, err := p.approvedBy(p.head, ledger.ReviewApproves, 1)
	if err != nil || old.Merged() {
		return Violatedf("the approval of the old head reads %+v: %v", old, err)
	}
	return p.landed(merge, "merged-moved")
}

func checkMovedPlanningBranch(r *Rig) error {
	p, err := newPlanMergeRig(r, ledger.ReviewOnBranch, ledger.ReviewApproves, false)
	if err != nil {
		return err
	}
	if _, err := p.approved(); err != nil {
		return err
	}
	if _, err := p.tick(); err != nil {
		return err
	}
	merge, err := p.oneMerge()
	if err != nil {
		return err
	}
	moved := p.movedPlan()
	refused := ledger.MergeResult{Outcome: ledger.MergeStaleHead, Head: moved, Detail: "branch " + branchName + " is at " + moved + ", not the reviewed commit " + p.head}
	if err := p.mergeCame(merge, refused); err != nil {
		return err
	}
	if err := p.noMerge(); err != nil {
		return err
	}
	pending, err := p.askedAgainAt(moved)
	if err != nil {
		return err
	}
	if _, err := p.r.approve(p.store, pending, true, theMember); err != nil {
		return err
	}
	if _, err := p.tick(); err != nil {
		return err
	}
	again, err := p.oneMerge()
	if err != nil || again.Commit != moved {
		return Violatedf("the worker is asked to merge %+v, want the new head %s: %v", again, moved, err)
	}
	return p.landed(again, "merged-branch")
}

func checkPlanningConflict(r *Rig) error {
	p, err := newPlanMergeRig(r, ledger.ReviewOnBranch, ledger.ReviewApproves, false)
	if err != nil {
		return err
	}
	if _, err := p.approved(); err != nil {
		return err
	}
	if _, err := p.tick(); err != nil {
		return err
	}
	merge, err := p.oneMerge()
	if err != nil {
		return err
	}
	conflict := ledger.MergeResult{Outcome: ledger.MergeConflict, Files: []string{"plan.md"}, Detail: "the branch does not merge cleanly into main"}
	if err := p.mergeCame(merge, conflict); err != nil {
		return err
	}
	if err := p.stands(workorders.Ready); err != nil {
		return err
	}
	order, err := p.order()
	if err != nil || order.Branch != branchName {
		return Violatedf("the planning work continues %q: %v", order.Branch, err)
	}
	note, err := p.reworkNote()
	if err != nil {
		return err
	}
	for _, want := range []string{"conflicts with main", "plan.md", "Merge the current main into this branch"} {
		if !strings.Contains(note.Reason, want) {
			return Violatedf("the rework's brief leaves out %q: %q", want, note.Reason)
		}
	}
	if err := p.noMerge(); err != nil {
		return err
	}
	return r.CheckProjection(p.store)
}

func checkAdoptionBranchMerges(r *Rig) error {
	p, err := newPlanMergeRig(r, ledger.ReviewOnBranch, ledger.PersonApproves, true)
	if err != nil {
		return err
	}
	pending, err := p.asked()
	if err != nil {
		return err
	}
	if planDetailOf(pending).Adoption == nil {
		return Violatedf("the adoption's approval does not group the plan: %+v", pending)
	}
	if err := p.noMerge(); err != nil {
		return err
	}
	if _, err := p.r.approve(p.store, pending, true, theMember); err != nil {
		return err
	}
	if _, err := p.tick(); err != nil {
		return err
	}
	if _, err := p.approvedBy(p.head, ledger.PersonApproves, 1); err != nil {
		return err
	}
	merge, err := p.oneMerge()
	if err != nil {
		return err
	}
	if merge.Commit != p.head || merge.Branch != branchName || merge.Base != "main" {
		return Violatedf("the worker is asked to merge %+v", merge)
	}
	if err := p.noMergeRequest(); err != nil {
		return err
	}
	return p.landed(merge, "merged-adoption")
}

func checkPlanAcceptedByHandWaits(r *Rig) error {
	p, err := newPlanMergeRig(r, ledger.ReviewOnBranch, ledger.ReviewApproves, false)
	if err != nil {
		return err
	}
	if _, err := p.asked(); err != nil {
		return err
	}
	ops := r.NewOps(p.store)
	if _, err := ops.Accept(r.Ctx, r.Scope, p.grant.WorkOrder, theMember); err != nil {
		return err
	}
	if err := p.stands(workorders.Integrating); err != nil {
		return err
	}
	// Intake records the plan the member accepted, and asks for the approval of the version it recorded.
	if _, err := p.tick(); err != nil {
		return err
	}
	if err := p.noMerge(); err != nil {
		return err
	}
	recorded, err := r.theOpenPlanApproval(p.store)
	if err != nil {
		return err
	}
	if view, err := p.standing(); err != nil || !strings.Contains(view.Why, recorded.Subject) {
		return Violatedf("the merge waits on %q, want the plan %s: %v", view.Why, recorded.Subject, err)
	}
	// A member's acceptance of the integrating plan is refused, and approves nothing.
	if _, err := ops.Accept(r.Ctx, r.Scope, p.grant.WorkOrder, theMember); !errors.Is(err, workorders.ErrNotAcceptable) {
		return Violatedf("accepting integrating planning work answered %v", err)
	}
	if records, err := p.integrations(); err != nil || len(records) != 0 {
		return Violatedf("approvals on record before the plan's approval: %+v, %v", records, err)
	}
	if err := p.noMergeRequest(); err != nil {
		return err
	}
	if _, err := r.approve(p.store, recorded, true, theMember); err != nil {
		return err
	}
	merge, err := p.oneMerge()
	if err != nil || merge.Commit != p.head {
		return Violatedf("the worker is asked to merge %+v: %v", merge, err)
	}
	if _, err := p.approvedBy(p.head, ledger.ReviewApproves, 1); err != nil {
		return err
	}
	return p.landed(merge, "merged-by-hand")
}
