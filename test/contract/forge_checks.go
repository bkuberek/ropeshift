package contract

import (
	"encoding/json"
	"errors"
	"strings"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ForgeChecks are what the ledger promises about review through a pull request (ADR-0020, FR-MERGE-1,
// FR-MERGE-10, FR-MERGE-12): the worker that ran a lane is asked to push its branch and open the pull request,
// the review waits for that pull request at the commit it judges, and a worker's word on a forge moves
// only what it answers.
var ForgeChecks = []Check{
	{"FG1", "a completed change in a repository that reviews through pull requests asks its worker to publish the commit, and holds its review", checkForgePublishRequested},
	{"FG2", "a pull request open at the commit under review releases the review, and a stale or a stranger's report changes nothing", checkForgeReportReleases},
	{"FG3", "a failed attempt keeps why and offers the next, and the review says so", checkForgeFailedAttempt},
	{"FG4", "a worker whose gh cannot run holds the review as forge_unavailable until its sign-in mends", checkForgeUnavailableHolds},
	{"FG5", "a repository that reviews branches asks for nothing and holds no review", checkForgeBranchTarget},
	{"FG6", "review target pull_request needs a forge binding, which a remote on a forge gives or a member names", checkForgeNeedsBinding},
}

const forgeRemote = "git@github.com:acme/widgets.git"

// forgeRig is a registered worker that ran a change lane to completion, in a repository under a review target.
type forgeRig struct {
	*statusRig
	repository string
	head       string
}

func newForgeRig(r *Rig, target ledger.ReviewTarget) (*forgeRig, error) {
	s := r.Store()
	repository := r.ID("repo")
	if _, err := r.detect(s, repository, forgeRemote, "main"); err != nil {
		return nil, err
	}
	if _, err := r.change(s, repository, control.RepositoryChange{ReviewTarget: &target}, r.Actor); err != nil {
		return nil, err
	}
	ready := func(store ports.Store) (string, error) {
		ops := r.NewOps(store)
		order, err := ops.Record(r.Ctx, r.Scope, control.LedgerWork{
			Order:     workorders.WorkOrder{ID: r.ID("wo"), Kind: workorders.KindChange, Title: "Add the retry budget", RepositoryID: repository, MaxAttempts: 2},
			Statement: "Retry a failed payment within a budget.",
			Criteria:  []planning.Criterion{{ID: "c1", Text: "A failed payment is retried"}, {ID: "c2", Text: "Each retry is logged"}},
		}, r.Actor)
		if err != nil {
			return "", err
		}
		_, err = ops.Pass(r.Ctx, r.Scope, order.ID, r.Actor)
		return order.ID, err
	}
	st, err := newStatusRigOn(r, s, ready)
	if err != nil {
		return nil, err
	}
	f := &forgeRig{statusRig: st, repository: repository, head: r.ID("commit")}
	raw, err := json.Marshal(LaneFinished(f.head))
	if err != nil {
		return nil, err
	}
	reply, err := f.upload(ledger.UploadLaneEnded, raw)
	if err != nil || len(reply.Rejected) != 0 {
		return nil, Violatedf("the lane's end was refused: %+v, %v", reply.Rejected, err)
	}
	return f, nil
}

// review is the one review of the rig's work order.
func (f *forgeRig) review() (workorders.WorkOrder, error) {
	return f.r.theReview(f.store, f.grant.WorkOrder)
}

// desired is what the control plane asks of the rig's worker now.
func (f *forgeRig) desired() (ledger.DesiredState, error) {
	return f.plane.Desired(f.r.Ctx, ledger.DesiredRequest{Auth: f.auth})
}

// forgeUpload sends one forge word as the rig's worker, or as another.
func (f *forgeRig) forgeUpload(auth ledger.WorkerAuth, epoch int64, eventType string, payload any) (ledger.UploadReply, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return ledger.UploadReply{}, err
	}
	f.seq++
	return f.plane.Upload(f.r.Ctx, ledger.UploadBatch{Auth: auth, Epoch: epoch, Events: []ledger.UploadEvent{{
		Epoch: epoch, Seq: f.seq, Type: eventType, Payload: raw, OccurredAt: 1,
	}}})
}

func (f *forgeRig) report(report ledger.ForgeReport) error {
	reply, err := f.forgeUpload(f.auth, f.epoch, ledger.UploadForgeReported, report)
	if err == nil && len(reply.Rejected) != 0 {
		return Violatedf("a forge report was refused: %+v", reply.Rejected)
	}
	return err
}

func (f *forgeRig) health(state ledger.ForgeHealthState, detail string) error {
	reply, err := f.forgeUpload(f.auth, f.epoch, ledger.UploadForgeHealth, ledger.ForgeHealthReport{Repository: f.repository, State: state, Detail: detail})
	if err == nil && len(reply.Rejected) != 0 {
		return Violatedf("a health report was refused: %+v", reply.Rejected)
	}
	return err
}

// heldAs says the review is held for a reason whose detail says what is held.
func (f *forgeRig) heldAs(reason workorders.HoldReason, detail string) error {
	review, err := f.review()
	if err != nil {
		return err
	}
	if review.State != workorders.Ready || review.StartHeld == nil || review.StartHeld.Reason != reason || !strings.Contains(review.StartHeld.Detail, detail) {
		return Violatedf("the review reads %s held by %+v, want %s naming %q", review.State, review.StartHeld, reason, detail)
	}
	_, err = f.plane.Claim(f.r.Ctx, f.auth, workorders.ClaimRequest{WorkOrder: review.ID, ClaimID: f.r.ID("claim"), TTLMs: TTLMs})
	if !errors.Is(err, workorders.ErrIneligible) || !strings.Contains(err.Error(), string(reason)) {
		return Violatedf("a claim of the held review answered %v", err)
	}
	return nil
}

func (f *forgeRig) publish() (ledger.DesiredForge, error) {
	desired, err := f.desired()
	if err != nil {
		return ledger.DesiredForge{}, err
	}
	if len(desired.Forge) != 1 {
		return ledger.DesiredForge{}, Violatedf("the worker is asked %+v, want one publish", desired.Forge)
	}
	return desired.Forge[0], nil
}

func (f *forgeRig) opened(attempt int64, head string) ledger.ForgeReport {
	return ledger.ForgeReport{Request: "publish:" + f.grant.WorkOrder + ":" + f.head, Attempt: attempt, PullRequest: &ledger.PullRequest{
		Number: 7, URL: "https://github.com/acme/widgets/pull/7", State: ledger.PullRequestOpen, Base: "main", Branch: branchName, HeadCommit: head,
	}}
}

func checkForgePublishRequested(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	publish, err := f.publish()
	if err != nil {
		return err
	}
	if publish.Kind != ledger.ForgePublish || publish.WorkOrder != f.grant.WorkOrder || publish.Repository != f.repository || publish.Binding != "github.com/acme/widgets" ||
		publish.Branch != branchName || publish.Base != "main" || publish.Commit != f.head || publish.Attempt != 1 || publish.Title != "Add the retry budget" {
		return Violatedf("the worker is asked %+v", publish)
	}
	for _, want := range []string{f.grant.WorkOrder, "Retry a failed payment within a budget.", "`c1`: A failed payment is retried", "`c2`: Each retry is logged"} {
		if !strings.Contains(publish.Body, want) {
			return Violatedf("the pull request's description leaves out %q:\n%s", want, publish.Body)
		}
	}
	desired, err := f.desired()
	if err != nil {
		return err
	}
	if len(desired.ForgeRepositories) != 1 || desired.ForgeRepositories[0] != (ledger.DesiredForgeRepository{Repository: f.repository, Binding: "github.com/acme/widgets"}) {
		return Violatedf("the worker is asked for the health of %+v", desired.ForgeRepositories)
	}
	if count, event, err := r.eventsAbout(f.store, f.grant.WorkOrder, ledger.EventForgeRequested); err != nil || count != 1 || event.Actor.Kind != ledger.ActorLane {
		return Violatedf("%d requests on record, the last by %+v: %v", count, event.Actor, err)
	}
	return f.heldAs(workorders.HoldPullRequestPending, "waiting for worker w to push "+branchName)
}

func checkForgeReportReleases(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	// A stranger cannot answer for this worker.
	other, err := newStatusRig(r)
	if err != nil {
		return err
	}
	stranger, err := f.forgeUpload(other.auth, other.epoch, ledger.UploadForgeReported, f.opened(1, f.head))
	if err == nil && (len(stranger.Rejected) != 1 || stranger.Rejected[0].Status != ledger.UploadInvalid) {
		return Violatedf("a stranger's report was answered %+v", stranger.Rejected)
	}
	if err != nil && !errors.Is(err, ledger.ErrBadCredential) {
		return err
	}
	if err := f.heldAs(workorders.HoldPullRequestPending, branchName); err != nil {
		return err
	}
	if err := f.report(f.opened(1, f.head)); err != nil {
		return err
	}
	review, err := f.review()
	if err != nil {
		return err
	}
	if review.StartHeld != nil {
		return Violatedf("the review is still held by %+v", review.StartHeld)
	}
	var pr ledger.WorkPullRequest
	err = f.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		pr, err = tx.Forge().PullRequest(f.grant.WorkOrder)
		return err
	})
	if err != nil || pr.Number != 7 || pr.HeadCommit != f.head || pr.Worker != f.auth.WorkerID || pr.State != ledger.PullRequestOpen {
		return Violatedf("the pull request on record is %+v: %v", pr, err)
	}
	if desired, err := f.desired(); err != nil || len(desired.Forge) != 0 {
		return Violatedf("the worker is still asked %+v: %v", desired.Forge, err)
	}
	// The work keeps the pull request's branch for a rework to continue, and the ended lane's worktree goes,
	// which frees that branch.
	if work, err := r.WorkOrder(f.store, f.grant.WorkOrder); err != nil || work.Branch != branchName {
		return Violatedf("the work keeps branch %q: %v", work.Branch, err)
	}
	if count, event, err := r.eventsAbout(f.store, f.lane, ledger.EventLaneArchiveRequested); err != nil || count != 1 || event.Actor.ID != "forge" {
		return Violatedf("%d archive requests for the lane, the last by %+v: %v", count, event.Actor, err)
	}
	// The same attempt again, and an older one, change nothing.
	before, err := r.Revision(f.store, r.Scope)
	if err != nil {
		return err
	}
	if err := f.report(f.opened(1, "another-head")); err != nil {
		return err
	}
	if count, _, err := r.eventsAbout(f.store, f.grant.WorkOrder, ledger.EventPullRequestOpened); err != nil || count != 1 {
		return Violatedf("%d openings on record: %v", count, err)
	}
	after, err := r.Revision(f.store, r.Scope)
	if err != nil {
		return err
	}
	if after != before {
		return Violatedf("a report of a request already done moved the revision from %d to %d", before, after)
	}
	grant, err := f.plane.Claim(r.Ctx, f.auth, workorders.ClaimRequest{WorkOrder: review.ID, ClaimID: r.ID("claim"), TTLMs: TTLMs})
	if err != nil || grant.WorkOrder != review.ID {
		return Violatedf("the released review could not be claimed: %v", err)
	}
	return r.CheckProjection(f.store)
}

func checkForgeFailedAttempt(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	if err := f.report(ledger.ForgeReport{Request: "publish:" + f.grant.WorkOrder + ":" + f.head, Attempt: 1, Failed: true, Detail: "push refused: protected branch"}); err != nil {
		return err
	}
	publish, err := f.publish()
	if err != nil {
		return err
	}
	if publish.Attempt != 2 {
		return Violatedf("after a failure the worker is asked attempt %d", publish.Attempt)
	}
	if count, _, err := r.eventsAbout(f.store, f.grant.WorkOrder, ledger.EventForgeFailed); err != nil || count != 1 {
		return Violatedf("%d failures on record: %v", count, err)
	}
	if err := f.heldAs(workorders.HoldPullRequestPending, "push refused: protected branch"); err != nil {
		return err
	}
	// A pull request whose head is not yet the commit under review leaves the review held.
	if err := f.report(f.opened(2, "an-older-head")); err != nil {
		return err
	}
	return f.heldAs(workorders.HoldPullRequestPending, "not yet the commit under review")
}

func checkForgeUnavailableHolds(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	if err := f.health(ledger.ForgeSignedOut, "the token in keyring is invalid"); err != nil {
		return err
	}
	if err := f.heldAs(workorders.HoldForgeUnavailable, "the token in keyring is invalid"); err != nil {
		return err
	}
	var health []ledger.ForgeHealth
	err = f.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		health, err = tx.Forge().Health()
		return err
	})
	if err != nil || len(health) != 1 || health[0].Worker != f.auth.WorkerID || health[0].State != ledger.ForgeSignedOut {
		return Violatedf("the health on record is %+v: %v", health, err)
	}
	// The same word again appends nothing.
	if err := f.health(ledger.ForgeSignedOut, "the token in keyring is invalid"); err != nil {
		return err
	}
	if count, _, err := r.eventsAbout(f.store, f.repository, ledger.EventForgeHealth); err != nil || count != 1 {
		return Violatedf("%d health changes on record: %v", count, err)
	}
	if err := f.health(ledger.ForgeHealthy, ""); err != nil {
		return err
	}
	return f.heldAs(workorders.HoldPullRequestPending, branchName)
}

func checkForgeBranchTarget(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnBranch)
	if err != nil {
		return err
	}
	desired, err := f.desired()
	if err != nil {
		return err
	}
	if len(desired.Forge) != 0 || len(desired.ForgeRepositories) != 0 {
		return Violatedf("a branch review asks the worker %+v and %+v", desired.Forge, desired.ForgeRepositories)
	}
	review, err := f.review()
	if err != nil {
		return err
	}
	if review.StartHeld != nil {
		return Violatedf("a branch review is held by %+v", review.StartHeld)
	}
	return nil
}

func checkForgeNeedsBinding(r *Rig) error {
	s := r.Store()
	pullRequest := ledger.ReviewOnPullRequest
	if _, err := r.detect(s, "repo-local", "/srv/git/widgets.git", "main"); err != nil {
		return err
	}
	if _, err := r.change(s, "repo-local", control.RepositoryChange{ReviewTarget: &pullRequest}, r.Actor); !errors.Is(err, ledger.ErrInvalidRepository) {
		return Violatedf("pull_request with no binding answered %v", err)
	}
	named := "github.com/acme/widgets"
	record, err := r.change(s, "repo-local", control.RepositoryChange{ReviewTarget: &pullRequest, ForgeBinding: &named}, r.Actor)
	if err != nil || record.ForgeBinding != named || record.Target() != ledger.ReviewOnPullRequest {
		return Violatedf("a named binding left %+v: %v", record, err)
	}
	bad := "acme/widgets"
	if _, err := r.change(s, "repo-local", control.RepositoryChange{ForgeBinding: &bad}, r.Actor); !errors.Is(err, ledger.ErrInvalidRepository) {
		return Violatedf("a binding with no host answered %v", err)
	}
	if _, err := r.detect(s, "repo-forge", "https://github.com/acme/gadgets.git", "main"); err != nil {
		return err
	}
	record, err = r.change(s, "repo-forge", control.RepositoryChange{ReviewTarget: &pullRequest}, r.Actor)
	if err != nil || record.ForgeBinding != "github.com/acme/gadgets" {
		return Violatedf("a remote on a forge left %+v: %v", record, err)
	}
	return nil
}

// ForgeMergeChecks are the steps after a pull request opens (FR-REVIEW-9, FR-MERGE-9, FR-MERGE-10): the
// verdicts go on the pull request as a comment, rework updates the same pull request, the worker watches it,
// and at person_merges the integration tick ends the work the person merged.
var ForgeMergeChecks = []Check{
	{"FG7", "a review's verdicts are asked of the worker that opened the pull request, as one comment bound to the commit", checkForgeVerdictComment},
	{"FG8", "the worker that opened a pull request watches it while its work is open, and a read that changes nothing writes nothing", checkForgeWatch},
	{"FG9", "at person_merges a merge the person made ends the work, by the integration tick, and a closed pull request ends nothing", checkForgeMergeEndsWork},
	{"FG10", "rework publishes the same branch again, the same pull request moves to the new head, and its review waits for it", checkForgeReworkUpdatesThePullRequest},
}

// reviewed has the rig's worker report the pull request open at the commit under review, and runs the review
// with these verdicts on another worker's lane.
func (f *forgeRig) reviewed(verdicts ...workorders.CriterionVerdict) (workorders.WorkOrder, error) {
	if err := f.report(f.opened(1, f.head)); err != nil {
		return workorders.WorkOrder{}, err
	}
	review, err := f.review()
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	holder, err := f.r.Running(f.store, review.ID, "reviewer")
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	result := laneResult(workorders.OutcomeCompleted, "")
	result.Verdicts = verdicts
	if err := f.r.NewOps(f.store).EndLane(f.r.Ctx, f.r.Scope, holder, result, f.r.worker("reviewer")); err != nil {
		return workorders.WorkOrder{}, err
	}
	return review, nil
}

func passes(criterion, reasons string) workorders.CriterionVerdict {
	return workorders.CriterionVerdict{Criterion: criterion, Verdict: workorders.VerdictPass, Reasons: reasons, Citations: []string{"pay/retry.go:12"}}
}

func (f *forgeRig) read(pr ledger.PullRequest) error {
	reply, err := f.forgeUpload(f.auth, f.epoch, ledger.UploadPullRequestRead, ledger.PullRequestReport{WorkOrder: f.grant.WorkOrder, PullRequest: pr})
	if err == nil && len(reply.Rejected) != 0 {
		return Violatedf("a read was refused: %+v", reply.Rejected)
	}
	return err
}

func (f *forgeRig) integrate() ([]string, error) {
	return control.NewIntegrations(f.store).Settle(f.r.Ctx, f.r.Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "integration-tick"})
}

func checkForgeVerdictComment(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	review, err := f.reviewed(passes("c1", "Each failed payment is retried."), passes("c2", "Each retry logs a line."))
	if err != nil {
		return err
	}
	desired, err := f.desired()
	if err != nil {
		return err
	}
	if len(desired.Forge) != 1 {
		return Violatedf("after the review the worker is asked %+v, want one comment", desired.Forge)
	}
	comment := desired.Forge[0]
	if comment.Kind != ledger.ForgeComment || comment.Number != 7 || comment.Commit != f.head || comment.Binding != "github.com/acme/widgets" || comment.ID != "comment:"+review.ID {
		return Violatedf("the worker is asked %+v", comment)
	}
	for _, want := range []string{review.ID, f.head[:8], "`c1`", "pass", "Each failed payment is retried.", "pay/retry.go:12", "`c2`", "Each retry logs a line.", "waits for a member"} {
		if !strings.Contains(comment.Body, want) {
			return Violatedf("the comment leaves out %q:\n%s", want, comment.Body)
		}
	}
	if err := f.report(ledger.ForgeReport{Request: comment.ID, Attempt: 1}); err != nil {
		return err
	}
	if count, _, err := r.eventsAbout(f.store, f.grant.WorkOrder, ledger.EventPullRequestCommented); err != nil || count != 1 {
		return Violatedf("%d comments on record: %v", count, err)
	}
	// The verdicts stay in the ledger, bound to the commit, as they always were.
	view, err := r.NewOps(f.store).Standing(r.Ctx, r.Scope, f.grant.WorkOrder)
	if err != nil || len(view.Verdicts) != 2 || view.Verdicts[0].Commit != f.head {
		return Violatedf("the verdicts on record are %+v: %v", view.Verdicts, err)
	}
	return nil
}

func checkForgeWatch(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	if _, err := f.reviewed(passes("c1", "ok"), passes("c2", "ok")); err != nil {
		return err
	}
	desired, err := f.desired()
	if err != nil {
		return err
	}
	if len(desired.PullRequests) != 1 {
		return Violatedf("the worker watches %+v", desired.PullRequests)
	}
	watched := desired.PullRequests[0]
	if watched.WorkOrder != f.grant.WorkOrder || watched.Number != 7 || watched.Binding != "github.com/acme/widgets" || watched.HeadCommit != f.head || watched.State != ledger.PullRequestOpen {
		return Violatedf("the worker watches %+v", watched)
	}
	before, err := r.Revision(f.store, r.Scope)
	if err != nil {
		return err
	}
	same := f.opened(1, f.head).PullRequest
	if err := f.read(*same); err != nil {
		return err
	}
	if after, err := r.Revision(f.store, r.Scope); err != nil || after != before {
		return Violatedf("a read that changed nothing moved the revision from %d to %d: %v", before, after, err)
	}
	checked := *same
	checked.Checks = []ledger.PullRequestCheck{{Name: "unit", State: ledger.CheckPassed}}
	if err := f.read(checked); err != nil {
		return err
	}
	if count, _, err := r.eventsAbout(f.store, f.grant.WorkOrder, ledger.EventPullRequestUpdated); err != nil || count != 1 {
		return Violatedf("%d updates on record: %v", count, err)
	}
	// Another worker's read is refused.
	other, err := newStatusRig(r)
	if err != nil {
		return err
	}
	reply, err := f.forgeUpload(other.auth, other.epoch, ledger.UploadPullRequestRead, ledger.PullRequestReport{WorkOrder: f.grant.WorkOrder, PullRequest: checked})
	if err != nil || len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadInvalid {
		return Violatedf("a stranger's read was answered %+v: %v", reply.Rejected, err)
	}
	return nil
}

func checkForgeMergeEndsWork(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	if _, err := f.reviewed(passes("c1", "ok"), passes("c2", "ok")); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Acceptance); err != nil {
		return err
	}
	if moved, err := f.integrate(); err != nil || len(moved) != 0 {
		return Violatedf("an open pull request ended %v: %v", moved, err)
	}
	closed := *f.opened(1, f.head).PullRequest
	closed.State = ledger.PullRequestClosed
	if err := f.read(closed); err != nil {
		return err
	}
	if moved, err := f.integrate(); err != nil || len(moved) != 0 {
		return Violatedf("a closed pull request ended %v: %v", moved, err)
	}
	merged := closed
	merged.State, merged.MergeCommit, merged.MergedAt = ledger.PullRequestMerged, r.ID("merge"), "2026-01-02T03:04:05Z"
	if err := f.read(merged); err != nil {
		return err
	}
	moved, err := f.integrate()
	if err != nil || len(moved) != 1 || moved[0] != f.grant.WorkOrder {
		return Violatedf("the merge ended %v: %v", moved, err)
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Done); err != nil {
		return err
	}
	count, event, err := r.eventsAbout(f.store, f.grant.WorkOrder, workorders.EventType(workorders.TriggerAcceptancePassed))
	var change workorders.Change
	if err == nil {
		err = json.Unmarshal(event.Data, &change)
	}
	if err != nil || count != 1 || event.Actor.ID != "integration-tick" || !strings.Contains(string(change.Payload), merged.MergeCommit) {
		return Violatedf("%d acceptances, the last %+v: %v", count, event, err)
	}
	if count, _, err := r.eventsAbout(f.store, f.grant.WorkOrder, ledger.EventPullRequestMerged); err != nil || count != 1 {
		return Violatedf("%d merges on record: %v", count, err)
	}
	// The person's merge counts as accepting after the review's pass (ADR-0019).
	var calibrations []workorders.Calibration
	err = f.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		calibrations, err = tx.Evidence().Calibrations()
		return err
	})
	if err != nil || len(calibrations) != 1 || calibrations[0].Decision != workorders.DecisionAccept || calibrations[0].Agreement != workorders.AgreementAgree ||
		calibrations[0].Commit != f.head || !strings.Contains(calibrations[0].Reason, "#7") {
		return Violatedf("the calibrations on record are %+v: %v", calibrations, err)
	}
	if desired, err := f.desired(); err != nil || len(desired.PullRequests) != 0 {
		return Violatedf("done work is still watched: %+v: %v", desired.PullRequests, err)
	}
	if moved, err := f.integrate(); err != nil || len(moved) != 0 {
		return Violatedf("a second tick ended %v: %v", moved, err)
	}
	return r.CheckProjection(f.store)
}

func checkForgeReworkUpdatesThePullRequest(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	failed := workorders.CriterionVerdict{Criterion: "c1", Verdict: workorders.VerdictFail, Reasons: "No retry.", Citations: []string{"pay/retry.go:3"}}
	if _, err := f.reviewed(failed, passes("c2", "ok")); err != nil {
		return err
	}
	if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Ready); err != nil {
		return err
	}
	comment, err := f.publish()
	if err != nil {
		return err
	}
	if !strings.Contains(comment.Body, "No retry.") || !strings.Contains(comment.Body, "goes back for rework") {
		return Violatedf("the comment of a failed review reads:\n%s", comment.Body)
	}
	if err := f.report(ledger.ForgeReport{Request: comment.ID, Attempt: 1}); err != nil {
		return err
	}
	// The next attempt runs on this worker again and completes with a new commit on the same branch.
	f.grant, err = f.plane.Claim(r.Ctx, f.auth, workorders.ClaimRequest{WorkOrder: f.grant.WorkOrder, ClaimID: r.ID("claim"), TTLMs: TTLMs})
	if err != nil {
		return err
	}
	f.lane = r.ID("lane")
	ops := r.NewOps(f.store)
	if _, err := ops.Dispatch(r.Ctx, r.Scope, f.grant.WorkOrder, f.lane, r.Actor); err != nil {
		return err
	}
	if _, err := ops.StartLane(r.Ctx, r.Scope, f.grant.Lease, f.lane, r.Actor); err != nil {
		return err
	}
	again := r.ID("commit")
	raw, err := json.Marshal(LaneFinished(again))
	if err != nil {
		return err
	}
	if reply, err := f.upload(ledger.UploadLaneEnded, raw); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("the second lane's end was refused: %+v, %v", reply.Rejected, err)
	}
	publish, err := f.publish()
	if err != nil {
		return err
	}
	if publish.Kind != ledger.ForgePublish || publish.Branch != branchName || publish.Commit != again || publish.ID != "publish:"+f.grant.WorkOrder+":"+again {
		return Violatedf("the rework asks %+v", publish)
	}
	reviews, err := r.reviewsOf(f.store, f.grant.WorkOrder)
	if err != nil || len(reviews) != 2 {
		return Violatedf("reviews %+v: %v", reviews, err)
	}
	latest := reviews[1]
	if latest.StartHeld == nil || latest.StartHeld.Reason != workorders.HoldPullRequestPending || !strings.Contains(latest.StartHeld.Detail, "waiting for worker w to push "+branchName) {
		return Violatedf("the new review waits on %+v", latest.StartHeld)
	}
	// The worker finds the same pull request, now at the new head.
	f.head = again
	if err := f.report(f.opened(1, again)); err != nil {
		return err
	}
	reviews, err = r.reviewsOf(f.store, f.grant.WorkOrder)
	if err != nil || reviews[1].StartHeld != nil {
		return Violatedf("the new review is still held: %+v: %v", reviews, err)
	}
	var pr ledger.WorkPullRequest
	err = f.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		pr, err = tx.Forge().PullRequest(f.grant.WorkOrder)
		return err
	})
	if err != nil || pr.Number != 7 || pr.HeadCommit != again {
		return Violatedf("the pull request on record is %+v: %v", pr, err)
	}
	return nil
}
