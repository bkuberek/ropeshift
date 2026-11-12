package contract

import (
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// MergeChecks are what the ledger promises about merging accepted work (ADR-0020, FR-MERGE-2, FR-MERGE-3,
// FR-MERGE-5 to FR-MERGE-8, FR-MERGE-11): ropeshift asks a worker to merge only the reviewed commit, only
// once an approval binds to it and every required check on it is green, and every approval and merge leaves
// a record of who approved, at which level, and what landed.
var MergeChecks = []Check{
	{"MG1", "at review_approves the review's pass approves the commit, and the worker is asked to merge it with the method once its required checks are green", checkMergeAfterTheReview},
	{"MG2", "a required check that fails on the reviewed commit sends the work back, naming it, and nothing merges", checkRequiredCheckFailsTheMerge},
	{"MG3", "at person_approves only a member approves the merge, bound to the reviewed commit, and the approval asks for it", checkMemberApprovesTheMerge},
	{"MG4", "a merge refused for a moved head or by the forge waits for a member, who retries it or sends the work back", checkRefusedMergeWaits},
	{"MG5", "a conflict sends the work back to rework on the same branch, with the conflict in its brief", checkConflictReworksTheBranch},
	{"MG6", "with review target branch the lane's worker is asked to merge the branch into the base, and the merge ends the work", checkBranchMerge},
	{"MG7", "a failed check the base requires sends work waiting at person_merges back, and a check it does not require counts for nothing", checkRequiredChecksAreEvidence},
	{"MG8", "a pull request the person merges on the forge while its merge waits ends the work, and the merge is recorded", checkForgeMergeWhileIntegrating},
	{"MG9", "a merge asked of work a member sends back is offered no more, and a late refusal of it decides nothing", checkMergeOfWorkSentBack},
}

// newMergeRig is a forge rig whose repository merges at a level, with a method.
func newMergeRig(r *Rig, target ledger.ReviewTarget, level ledger.MergeAuthority, method ledger.MergeMethod) (*forgeRig, error) {
	f, err := newForgeRig(r, target)
	if err != nil {
		return nil, err
	}
	change := control.RepositoryChange{MergeAuthority: &level}
	if method != "" {
		change.MergeMethod = &method
	}
	_, err = r.change(f.store, f.repository, change, r.Actor)
	return f, err
}

// pullRequestAt is the rig's pull request open at a head, with checks.
func (f *forgeRig) pullRequestAt(head string, checks ...ledger.PullRequestCheck) ledger.PullRequest {
	pr := *f.opened(1, head).PullRequest
	pr.Checks = checks
	return pr
}

func requiredCheck(name string, state ledger.ForgeCheckState) ledger.PullRequestCheck {
	return ledger.PullRequestCheck{Name: name, State: state, Required: true, DetailsURI: "https://ci.example/" + name}
}

// merges lists the merge requests the rig's worker is asked to run.
func (f *forgeRig) merges() ([]ledger.DesiredForge, error) {
	desired, err := f.desired()
	if err != nil {
		return nil, err
	}
	var out []ledger.DesiredForge
	for _, request := range desired.Forge {
		if request.Kind == ledger.ForgeMerge {
			out = append(out, request)
		}
	}
	return out, nil
}

// oneMerge is the one merge request the rig's worker is asked to run.
func (f *forgeRig) oneMerge() (ledger.DesiredForge, error) {
	merges, err := f.merges()
	if err != nil {
		return ledger.DesiredForge{}, err
	}
	if len(merges) != 1 {
		return ledger.DesiredForge{}, Violatedf("the worker is asked to merge %+v, want one merge", merges)
	}
	return merges[0], nil
}

func (f *forgeRig) noMerge() error {
	if merges, err := f.merges(); err != nil || len(merges) != 0 {
		return Violatedf("the worker is asked to merge %+v: %v", merges, err)
	}
	return nil
}

// mergeCame reports what a merge came to, as the worker that ran it.
func (f *forgeRig) mergeCame(request ledger.DesiredForge, result ledger.MergeResult) error {
	return f.report(ledger.ForgeReport{Request: request.ID, Attempt: request.Attempt, Merge: &result})
}

func (f *forgeRig) standing() (workorders.AcceptanceView, error) {
	return f.r.NewOps(f.store).Standing(f.r.Ctx, f.r.Scope, f.grant.WorkOrder)
}

// mergeRequests lists the work's merge approval requests, oldest first.
func (f *forgeRig) mergeRequests() ([]authority.Approval, error) {
	all, err := control.NewApprovals(f.store).List(f.r.Ctx, f.r.Scope, "")
	var out []authority.Approval
	for _, row := range all {
		if row.Kind == authority.KindMerge && row.WorkOrder == f.grant.WorkOrder {
			out = append(out, row)
		}
	}
	return out, err
}

// openMergeRequest is the one open merge request of a subject.
func (f *forgeRig) openMergeRequest(subject string) (authority.Approval, error) {
	rows, err := f.mergeRequests()
	if err != nil {
		return authority.Approval{}, err
	}
	var open []authority.Approval
	for _, row := range rows {
		if row.Open() && row.Subject == subject {
			open = append(open, row)
		}
	}
	if len(open) != 1 {
		return authority.Approval{}, Violatedf("open merge requests %+v, want one asking %s", rows, subject)
	}
	return open[0], nil
}

func (f *forgeRig) decide(request authority.Approval, digest string, allow bool, reason string, actor ledger.Actor) (authority.Approval, error) {
	return control.NewApprovals(f.store).Decide(f.r.Ctx, f.r.Scope, authority.Decision{ID: request.ID, Digest: digest, Allow: allow, Reason: reason}, actor)
}

func (f *forgeRig) reworkNote() (workorders.ReworkNote, error) {
	var note workorders.ReworkNote
	err := f.store.Read(f.r.Ctx, f.r.Scope, func(tx ports.ReadTx) error {
		var err error
		note, err = tx.Evidence().Reworked(f.grant.WorkOrder)
		return err
	})
	return note, err
}

func checkMergeAfterTheReview(r *Rig) error {
	f, err := newMergeRig(r, ledger.ReviewOnPullRequest, ledger.ReviewApproves, ledger.MergeSquash)
	if err != nil {
		return err
	}
	// The worker reads the pull request while the review runs, with a check the base requires still running.
	if err := f.report(f.opened(1, f.head)); err != nil {
		return err
	}
	if err := f.read(f.pullRequestAt(f.head, requiredCheck("CI result", ledger.CheckPending), ledger.PullRequestCheck{Name: "lint", State: ledger.CheckFailed})); err != nil {
		return err
	}
	if _, err := f.judge(passes("c1", "ok"), passes("c2", "ok")); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Integrating); err != nil {
		return err
	}
	view, err := f.standing()
	if err != nil {
		return err
	}
	if len(view.Integrations) != 1 {
		return Violatedf("the approvals read %+v", view.Integrations)
	}
	approval := view.Integrations[0]
	if approval.ApprovedByKind != ledger.ApprovedByReview || approval.ApprovedCommit != f.head || approval.MergeAuthority != ledger.ReviewApproves ||
		approval.PullRequest != 7 || approval.ApprovedByID == "" || approval.Merged() {
		return Violatedf("the review's approval reads %+v", approval)
	}
	// A required check still running holds the merge, and says so.
	if err := f.noMerge(); err != nil {
		return err
	}
	if view, err = f.standing(); err != nil || !strings.Contains(view.Why, "CI result") {
		return Violatedf("the work waits on %q: %v", view.Why, err)
	}
	// Green, the worker that opened the pull request is asked to merge it at the reviewed head, with the method.
	if err := f.read(f.pullRequestAt(f.head, requiredCheck("CI result", ledger.CheckPassed), ledger.PullRequestCheck{Name: "lint", State: ledger.CheckFailed})); err != nil {
		return err
	}
	merge, err := f.oneMerge()
	if err != nil {
		return err
	}
	if merge.Number != 7 || merge.Commit != f.head || merge.Method != ledger.MergeSquash || merge.Binding != "github.com/acme/widgets" || merge.Base != "main" {
		return Violatedf("the worker is asked %+v", merge)
	}
	// The integration tick asks for nothing more.
	if moved, err := f.integrate(); err != nil || len(moved) != 0 {
		return Violatedf("a tick while the merge runs moved %v: %v", moved, err)
	}
	if err := f.noMergeBut(merge); err != nil {
		return err
	}
	landed := ledger.MergeResult{Outcome: ledger.MergeMerged, MergeCommit: r.ID("merged"), MergedAt: "2026-11-11T12:00:00Z"}
	if err := f.mergeCame(merge, landed); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Done); err != nil {
		return err
	}
	view, err = f.standing()
	if err != nil || len(view.Integrations) != 1 || view.Integrations[0].MergedCommit != landed.MergeCommit || !view.Integrations[0].Merged() {
		return Violatedf("after the merge the approvals read %+v: %v", view.Integrations, err)
	}
	count, event, err := r.eventsAbout(f.store, f.grant.WorkOrder, ledger.EventIntegrationMerged)
	if err != nil || count != 1 || !strings.Contains(string(event.Data), landed.MergeCommit) || !strings.Contains(string(event.Data), f.head) ||
		!strings.Contains(string(event.Data), string(ledger.ReviewApproves)) || !strings.Contains(string(event.Data), approval.ApprovedByID) {
		return Violatedf("%d merges on record, the last %s: %v", count, event.Data, err)
	}
	if count, _, err := r.eventsAbout(f.store, f.grant.WorkOrder, workorders.EventType(workorders.TriggerMerged)); err != nil || count != 1 {
		return Violatedf("%d moves to done by the merge: %v", count, err)
	}
	// The same report again changes nothing.
	before, err := r.Revision(f.store, r.Scope)
	if err != nil {
		return err
	}
	if err := f.mergeCame(merge, landed); err != nil {
		return err
	}
	if after, err := r.Revision(f.store, r.Scope); err != nil || after != before {
		return Violatedf("a repeated merge report moved the revision from %d to %d: %v", before, after, err)
	}
	return r.CheckProjection(f.store)
}

// noMergeBut says the worker is asked to run only this merge.
func (f *forgeRig) noMergeBut(want ledger.DesiredForge) error {
	merges, err := f.merges()
	if err != nil || len(merges) != 1 || merges[0].ID != want.ID {
		return Violatedf("the worker is asked to merge %+v: %v", merges, err)
	}
	return nil
}

func checkRequiredCheckFailsTheMerge(r *Rig) error {
	f, err := newMergeRig(r, ledger.ReviewOnPullRequest, ledger.ReviewApproves, "")
	if err != nil {
		return err
	}
	if err := f.report(f.opened(1, f.head)); err != nil {
		return err
	}
	if err := f.read(f.pullRequestAt(f.head, requiredCheck("CI result", ledger.CheckFailed), ledger.PullRequestCheck{Name: "lint", State: ledger.CheckPassed})); err != nil {
		return err
	}
	if _, err := f.judge(passes("c1", "ok"), passes("c2", "ok")); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Ready); err != nil {
		return err
	}
	if err := f.noMerge(); err != nil {
		return err
	}
	note, err := f.reworkNote()
	if err != nil || !strings.Contains(note.Reason, "CI result") || strings.Contains(note.Reason, "lint") {
		return Violatedf("the rework says %q: %v", note.Reason, err)
	}
	return r.CheckProjection(f.store)
}

func checkMemberApprovesTheMerge(r *Rig) error {
	f, err := newMergeRig(r, ledger.ReviewOnPullRequest, ledger.PersonApproves, "")
	if err != nil {
		return err
	}
	if _, err := f.reviewed(passes("c1", "ok"), passes("c2", "ok")); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Integrating); err != nil {
		return err
	}
	if err := f.noMerge(); err != nil {
		return err
	}
	request, err := f.openMergeRequest(authority.MergeApprove)
	if err != nil {
		return err
	}
	detail := authority.MergeDetailOf(request)
	if request.SubjectDigest != f.head || request.Lane != "" || detail.PullRequest != 7 || detail.Authority != ledger.PersonApproves || !strings.Contains(request.Summary, "#7") {
		return Violatedf("the merge request reads %+v with %+v", request, detail)
	}
	// The tick raises nothing more.
	if _, err := f.integrate(); err != nil {
		return err
	}
	if rows, err := f.mergeRequests(); err != nil || len(rows) != 1 {
		return Violatedf("after a tick the merge requests are %+v: %v", rows, err)
	}
	// Only a member decides it, and only on the commit it binds to.
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: f.auth.WorkerID}
	if _, err := f.decide(request, f.head, true, "", orchestrator); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("an orchestrator deciding a merge: %v", err)
	}
	if _, err := f.decide(request, r.ID("other"), true, "", r.Actor); !errors.Is(err, authority.ErrStaleDigest) {
		return Violatedf("a member deciding another commit: %v", err)
	}
	if err := f.noMerge(); err != nil {
		return err
	}
	if _, err := f.decide(request, f.head, true, "looks right", r.Actor); err != nil {
		return err
	}
	merge, err := f.oneMerge()
	if err != nil || merge.Commit != f.head || merge.Method != ledger.MergeCommit {
		return Violatedf("after the approval the worker is asked %+v: %v", merge, err)
	}
	view, err := f.standing()
	if err != nil || len(view.Integrations) != 1 || view.Integrations[0].ApprovedByKind != ledger.ApprovedByMember || view.Integrations[0].ApprovedByID != r.Actor.ID ||
		view.Integrations[0].MergeAuthority != ledger.PersonApproves {
		return Violatedf("the approvals read %+v: %v", view.Integrations, err)
	}
	calibrations, err := r.calibrationsOf(f.store, f.grant.WorkOrder)
	if err != nil || len(calibrations) != 1 || calibrations[0].Decision != workorders.DecisionAccept || calibrations[0].Commit != f.head {
		return Violatedf("the calibrations read %+v: %v", calibrations, err)
	}
	if err := f.mergeCame(merge, ledger.MergeResult{Outcome: ledger.MergeMerged, MergeCommit: r.ID("merged")}); err != nil {
		return err
	}
	return r.stateIs(f.store, f.grant.WorkOrder, workorders.Done)
}

func checkRefusedMergeWaits(r *Rig) error {
	f, err := newMergeRig(r, ledger.ReviewOnPullRequest, ledger.ReviewApproves, "")
	if err != nil {
		return err
	}
	if _, err := f.reviewed(passes("c1", "ok"), passes("c2", "ok")); err != nil {
		return err
	}
	first, err := f.oneMerge()
	if err != nil {
		return err
	}
	if err := f.mergeCame(first, ledger.MergeResult{Outcome: ledger.MergeStaleHead, Detail: "the pull request's head is 2222, not the reviewed commit"}); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Integrating); err != nil {
		return err
	}
	if err := f.noMerge(); err != nil {
		return err
	}
	held, err := f.openMergeRequest(authority.MergeHeld)
	if err != nil {
		return err
	}
	if held.SubjectDigest != f.head || !strings.Contains(held.Summary, "2222") || authority.MergeDetailOf(held).Request != first.ID {
		return Violatedf("the held merge reads %+v", held)
	}
	if view, err := f.standing(); err != nil || !strings.Contains(view.Why, "refused") {
		return Violatedf("the work waits on %q: %v", view.Why, err)
	}
	if _, err := f.integrate(); err != nil {
		return err
	}
	if err := f.noMerge(); err != nil {
		return err
	}
	// A member's retry asks for the merge again, as a new round.
	if _, err := f.decide(held, f.head, true, "", r.Actor); err != nil {
		return err
	}
	second, err := f.oneMerge()
	if err != nil || second.ID == first.ID || second.Commit != f.head {
		return Violatedf("a retry asks %+v after %+v: %v", second, first, err)
	}
	// Branch protection refuses it, and the member sends the work back.
	if err := f.mergeCame(second, ledger.MergeResult{Outcome: ledger.MergeRefused, Detail: "the base branch policy prohibits the merge"}); err != nil {
		return err
	}
	again, err := f.openMergeRequest(authority.MergeHeld)
	if err != nil || again.ID == held.ID || !strings.Contains(again.Summary, "base branch policy") {
		return Violatedf("the second hold reads %+v: %v", again, err)
	}
	if _, err := f.decide(again, f.head, false, "protection needs a review first", r.Actor); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Ready); err != nil {
		return err
	}
	note, err := f.reworkNote()
	if err != nil || !strings.Contains(note.Reason, "protection needs a review first") {
		return Violatedf("the rework says %q: %v", note.Reason, err)
	}
	if rows, err := f.mergeRequests(); err != nil || slices.ContainsFunc(rows, authority.Approval.Open) {
		return Violatedf("merge requests stay open after the rework: %+v: %v", rows, err)
	}
	return r.CheckProjection(f.store)
}

func checkConflictReworksTheBranch(r *Rig) error {
	f, err := newMergeRig(r, ledger.ReviewOnPullRequest, ledger.ReviewApproves, "")
	if err != nil {
		return err
	}
	if _, err := f.reviewed(passes("c1", "ok"), passes("c2", "ok")); err != nil {
		return err
	}
	merge, err := f.oneMerge()
	if err != nil {
		return err
	}
	conflict := ledger.MergeResult{Outcome: ledger.MergeConflict, Detail: "the merge commit cannot be cleanly created", Files: []string{"pay/retry.go"}}
	if err := f.mergeCame(merge, conflict); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Ready); err != nil {
		return err
	}
	order, err := r.WorkOrder(f.store, f.grant.WorkOrder)
	if err != nil || order.Branch != branchName {
		return Violatedf("the rework continues %q, want %s: %v", order.Branch, branchName, err)
	}
	note, err := f.reworkNote()
	if err != nil {
		return err
	}
	for _, want := range []string{"conflicts", "pay/retry.go", "Merge the current main into this branch", f.head[:8]} {
		if !strings.Contains(note.Reason, want) {
			return Violatedf("the rework says %q, which leaves out %q", note.Reason, want)
		}
	}
	return r.CheckProjection(f.store)
}

func checkBranchMerge(r *Rig) error {
	f, err := newMergeRig(r, ledger.ReviewOnBranch, ledger.ReviewApproves, ledger.MergeRebase)
	if err != nil {
		return err
	}
	if _, err := f.judge(passes("c1", "ok"), passes("c2", "ok")); err != nil {
		return err
	}
	merge, err := f.oneMerge()
	if err != nil {
		return err
	}
	if merge.Binding != "" || merge.Number != 0 || merge.Branch != branchName || merge.Base != "main" || merge.Commit != f.head || merge.Method != ledger.MergeRebase {
		return Violatedf("the lane's worker is asked %+v", merge)
	}
	if err := f.mergeCame(merge, ledger.MergeResult{Outcome: ledger.MergeMerged, MergeCommit: r.ID("merged")}); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Done); err != nil {
		return err
	}
	view, err := f.standing()
	if err != nil || len(view.Integrations) != 1 || view.Integrations[0].PullRequest != 0 || !view.Integrations[0].Merged() {
		return Violatedf("the approvals read %+v: %v", view.Integrations, err)
	}
	return r.CheckProjection(f.store)
}

func checkRequiredChecksAreEvidence(r *Rig) error {
	f, err := newMergeRig(r, ledger.ReviewOnPullRequest, ledger.PersonMerges, "")
	if err != nil {
		return err
	}
	if _, err := f.reviewed(passes("c1", "ok"), passes("c2", "ok")); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Acceptance); err != nil {
		return err
	}
	source := control.PullRequestChecks{Store: f.store}
	ops := r.NewOps(f.store)
	// Still running, a required check is no evidence yet, and the work waits.
	if err := f.read(f.pullRequestAt(f.head, requiredCheck("CI result", ledger.CheckPending), ledger.PullRequestCheck{Name: "lint", State: ledger.CheckFailed})); err != nil {
		return err
	}
	if moved, err := ops.Settle(r.Ctx, r.Scope, source, r.tick()); err != nil || len(moved) != 0 {
		return Violatedf("a pending required check moved %v: %v", moved, err)
	}
	if err := f.read(f.pullRequestAt(f.head, requiredCheck("CI result", ledger.CheckFailed), ledger.PullRequestCheck{Name: "lint", State: ledger.CheckFailed})); err != nil {
		return err
	}
	moved, err := ops.Settle(r.Ctx, r.Scope, source, r.tick())
	if err != nil || !slices.Equal(moved, []string{f.grant.WorkOrder}) {
		return Violatedf("a failed required check moved %v: %v", moved, err)
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Ready); err != nil {
		return err
	}
	view, err := f.standing()
	if err != nil {
		return err
	}
	var names []string
	for _, check := range view.Checks {
		if check.Source == workorders.SourceForge {
			names = append(names, check.Name)
		}
	}
	if !slices.Equal(names, []string{"CI result"}) {
		return Violatedf("the forge evidence on record is %v", names)
	}
	return nil
}

func checkForgeMergeWhileIntegrating(r *Rig) error {
	f, err := newMergeRig(r, ledger.ReviewOnPullRequest, ledger.PersonApproves, "")
	if err != nil {
		return err
	}
	if _, err := f.reviewed(passes("c1", "ok"), passes("c2", "ok")); err != nil {
		return err
	}
	if _, err := f.openMergeRequest(authority.MergeApprove); err != nil {
		return err
	}
	merged := f.pullRequestAt(f.head)
	merged.State, merged.MergeCommit, merged.MergedAt = ledger.PullRequestMerged, r.ID("merged"), "2026-11-11T12:00:00Z"
	if err := f.read(merged); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Done); err != nil {
		return err
	}
	view, err := f.standing()
	if err != nil || len(view.Integrations) != 1 || view.Integrations[0].ApprovedByKind != ledger.ApprovedByForge || view.Integrations[0].MergedCommit != merged.MergeCommit {
		return Violatedf("the approvals read %+v: %v", view.Integrations, err)
	}
	if rows, err := f.mergeRequests(); err != nil || len(rows) != 1 || rows[0].State != authority.ApprovalVoid {
		return Violatedf("the merge request after the forge's merge reads %+v: %v", rows, err)
	}
	return r.CheckProjection(f.store)
}

func checkMergeOfWorkSentBack(r *Rig) error {
	f, err := newMergeRig(r, ledger.ReviewOnPullRequest, ledger.ReviewApproves, "")
	if err != nil {
		return err
	}
	if _, err := f.reviewed(passes("c1", "ok"), passes("c2", "ok")); err != nil {
		return err
	}
	merge, err := f.oneMerge()
	if err != nil {
		return err
	}
	if _, err := r.NewOps(f.store).Rework(r.Ctx, r.Scope, f.grant.WorkOrder, "not what was asked", r.Actor); err != nil {
		return err
	}
	if err := f.noMerge(); err != nil {
		return err
	}
	// The worker had started it, and its refusal arrives after the work went back.
	if err := f.mergeCame(merge, ledger.MergeResult{Outcome: ledger.MergeRefused, Detail: "the base branch policy prohibits the merge"}); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Ready); err != nil {
		return err
	}
	if rows, err := f.mergeRequests(); err != nil || slices.ContainsFunc(rows, authority.Approval.Open) {
		return Violatedf("a late refusal raised merge requests %+v: %v", rows, err)
	}
	return r.CheckProjection(f.store)
}
