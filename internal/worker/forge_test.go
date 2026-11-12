package worker_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

var widgets = ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}

// forgeRig is a worker whose lane completes a change in a repository that reviews through pull requests,
// with the fake forge and a remote to push to.
type forgeRig struct {
	*laneRig
	forge     *fake.Forge
	worktrees *fake.Worktrees
}

func newForgeRig(t *testing.T, before func(*fake.Forge, *fake.Worktrees)) *forgeRig {
	t.Helper()
	base := newRig(t)
	repositories := control.NewRepositories(base.store)
	if _, err := repositories.Detect(t.Context(), scope, control.DetectedRepository{ID: "repo-1", Remote: "git@github.com:acme/widgets.git", DefaultBranch: "main"}, member); err != nil {
		t.Fatal(err)
	}
	target := ledger.ReviewOnPullRequest
	if _, err := repositories.Change(t.Context(), scope, "repo-1", control.RepositoryChange{ReviewTarget: &target}, member); err != nil {
		t.Fatal(err)
	}
	worktrees := fake.NewWorktrees()
	worktrees.UseRemote("origin")
	base.worktrees, base.repository = worktrees, "repo-1"
	r := newLaneRigFor(t, base)
	forge := &fake.Forge{}
	r.config.Forge = forge
	before(forge, worktrees)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	return &forgeRig{laneRig: r, forge: forge, worktrees: worktrees}
}

// converge runs passes until the ledger has heard what each pass did.
func (f *forgeRig) converge(passes int) {
	f.t.Helper()
	for range passes {
		if err := f.subject.Converge(f.t.Context()); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *forgeRig) review() workorders.WorkOrder {
	f.t.Helper()
	var out workorders.WorkOrder
	err := f.store.Read(f.t.Context(), scope, func(tx ports.ReadTx) error {
		reviews, err := tx.WorkOrders().ReviewsOf(f.id)
		if len(reviews) != 1 {
			f.t.Fatalf("%d reviews", len(reviews))
		}
		out = reviews[0]
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *forgeRig) pullRequest() (ledger.WorkPullRequest, error) {
	var pr ledger.WorkPullRequest
	err := f.store.Read(f.t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		pr, err = tx.Forge().PullRequest(f.id)
		return err
	})
	return pr, err
}

// TestAWorkerPublishesALanesCommitBeforeItsReview is FR-MERGE-10 and FR-MERGE-12 on the worker: the worker that
// ran the lane reads its forge sign-in, pushes the lane's branch, opens the pull request with the work
// order's description, and the review it waited for is free to start.
func TestAWorkerPublishesALanesCommitBeforeItsReview(t *testing.T) {
	t.Parallel()
	f := newForgeRig(t, func(*fake.Forge, *fake.Worktrees) {})
	if held := f.review().StartHeld; held == nil || held.Reason != workorders.HoldPullRequestPending {
		t.Fatalf("the review waits on %+v", held)
	}
	f.forge.Push(widgets, "lane/lane-1", "fake-head")
	f.converge(3)
	if pushed := f.worktrees.Pushed(); !slices.Contains(pushed, lanes.HeadsRef("lane/lane-1")) {
		t.Errorf("the worker pushed %v", pushed)
	}
	prs := f.forge.PullRequests()
	if len(prs) != 1 || prs[0].Branch != "lane/lane-1" || prs[0].Base != "main" {
		t.Fatalf("the forge holds %+v", prs)
	}
	pr, err := f.pullRequest()
	if err != nil || pr.Number != prs[0].Number || pr.HeadCommit != "fake-head" {
		t.Fatalf("the ledger holds %+v: %v", pr, err)
	}
	if held := f.review().StartHeld; held != nil {
		t.Errorf("the review is still held by %+v", held)
	}
	calls := f.forge.Calls()
	if len(calls) < 2 || !strings.HasPrefix(calls[0], "health ") || !slices.Contains(calls, "open lane/lane-1") {
		t.Errorf("the forge saw %v, and the sign-in is read before anything runs", calls)
	}
	// Nothing more is asked, and a later pass repeats nothing.
	f.converge(2)
	opens := 0
	for _, call := range f.forge.Calls() {
		if call == "open lane/lane-1" {
			opens++
		}
	}
	if opens != 1 {
		t.Errorf("the pull request was opened %d times", opens)
	}
}

// TestAWorkerWhoseGHCannotRunHoldsTheWork reads a sign-in with a problem as a health state of the
// repository on this worker: the request waits, and the review says why.
func TestAWorkerWhoseGHCannotRunHoldsTheWork(t *testing.T) {
	t.Parallel()
	f := newForgeRig(t, func(forge *fake.Forge, _ *fake.Worktrees) {
		forge.SignOut(widgets.Host, "the token in keyring is invalid")
	})
	f.converge(3)
	if pushed := f.worktrees.Pushed(); len(pushed) != 0 {
		t.Errorf("a worker with no working sign-in pushed %v", pushed)
	}
	held := f.review().StartHeld
	if held == nil || held.Reason != workorders.HoldForgeUnavailable || !strings.Contains(held.Detail, "the token in keyring is invalid") {
		t.Fatalf("the review waits on %+v", held)
	}
	var health []ledger.ForgeHealth
	err := f.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		health, err = tx.Forge().Health()
		return err
	})
	if err != nil || len(health) != 1 || health[0].State != ledger.ForgeSignedOut || health[0].Repository != "repo-1" {
		t.Fatalf("the health on record is %+v: %v", health, err)
	}
}

// TestAFailedPushIsReportedAndTriedAgainLater keeps a refused push as a failed attempt with its reason.
func TestAFailedPushIsReportedAndTriedAgainLater(t *testing.T) {
	t.Parallel()
	f := newForgeRig(t, func(_ *fake.Forge, worktrees *fake.Worktrees) { worktrees.RefusePushes(true) })
	f.converge(3)
	held := f.review().StartHeld
	if held == nil || held.Reason != workorders.HoldPullRequestPending || !strings.Contains(held.Detail, fake.ErrPushRefused.Error()) {
		t.Fatalf("the review waits on %+v", held)
	}
	if prs := f.forge.PullRequests(); len(prs) != 0 {
		t.Errorf("a refused push opened %+v", prs)
	}
}

// TestAWorkerPostsTheVerdictsAndReadsThePersonsMerge is FR-REVIEW-9 and FR-MERGE-9 on the worker: the review's
// verdicts reach the pull request as one comment, the worker watches the pull request, and the merge the person
// makes on the forge comes back, so the integration tick ends the work.
func TestAWorkerPostsTheVerdictsAndReadsThePersonsMerge(t *testing.T) {
	t.Parallel()
	f := newForgeRig(t, func(forge *fake.Forge, _ *fake.Worktrees) { forge.Push(widgets, "lane/lane-1", "fake-head") })
	f.converge(3)
	review := f.review()
	if review.StartHeld != nil {
		t.Fatalf("the review waits on %+v", review.StartHeld)
	}
	ctx := t.Context()
	leases := control.NewLeases(f.store)
	grant, err := leases.Claim(ctx, scope, workorders.ClaimRequest{WorkOrder: review.ID, ClaimID: "claim-review", Worker: "reviewer", TTLMs: 60_000}, member)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := leases.StartLane(ctx, scope, grant.Lease, "review-lane", member)
	if err != nil {
		t.Fatal(err)
	}
	result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted, Verdicts: []workorders.CriterionVerdict{
		{Criterion: "c1", Verdict: workorders.VerdictPass, Reasons: "It works as the criterion says.", Citations: []string{"main.go:3"}},
	}}}
	holder := workorders.Holder{Lease: grant.Lease, Lane: "review-lane", Generation: generation}
	if err := leases.EndLane(ctx, scope, holder, result, ledger.Actor{Kind: ledger.ActorLane, ID: "review-lane"}); err != nil {
		t.Fatal(err)
	}
	f.converge(2)
	number := f.forge.PullRequests()[0].Number
	comments := f.forge.Comments(number)
	if len(comments) != 1 || !strings.Contains(comments[0], "It works as the criterion says.") || !strings.Contains(comments[0], "**pass**") {
		t.Fatalf("the pull request holds the comments %q", comments)
	}
	if f.row().State != workorders.Acceptance {
		t.Fatalf("the work reads %s, and waits for the person at person_merges", f.row().State)
	}
	f.forge.MergeByPerson(number, "merge-commit")
	f.clock.Advance(2 * time.Minute)
	f.converge(2)
	pr, err := f.pullRequest()
	if err != nil || pr.State != ledger.PullRequestMerged || pr.MergeCommit != "merge-commit" {
		t.Fatalf("the ledger holds %+v: %v", pr, err)
	}
	moved, err := control.NewIntegrations(f.store).Settle(ctx, scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "integration-tick"})
	if err != nil || len(moved) != 1 || f.row().State != workorders.Done {
		t.Fatalf("the merge ended %v, and the work reads %s: %v", moved, f.row().State, err)
	}
}

// passReview runs the rig's review on a lane of another worker, which passes the one criterion.
func (f *forgeRig) passReview() {
	f.t.Helper()
	ctx := f.t.Context()
	review := f.review()
	leases := control.NewLeases(f.store)
	grant, err := leases.Claim(ctx, scope, workorders.ClaimRequest{WorkOrder: review.ID, ClaimID: "claim-review", Worker: "reviewer", TTLMs: 60_000}, member)
	if err != nil {
		f.t.Fatal(err)
	}
	generation, err := leases.StartLane(ctx, scope, grant.Lease, "review-lane", member)
	if err != nil {
		f.t.Fatal(err)
	}
	result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted, Verdicts: []workorders.CriterionVerdict{
		{Criterion: "c1", Verdict: workorders.VerdictPass, Reasons: "It works as the criterion says.", Citations: []string{"main.go:3"}},
	}}}
	holder := workorders.Holder{Lease: grant.Lease, Lane: "review-lane", Generation: generation}
	if err := leases.EndLane(ctx, scope, holder, result, ledger.Actor{Kind: ledger.ActorLane, ID: "review-lane"}); err != nil {
		f.t.Fatal(err)
	}
}

// TestAWorkerMergesAtTheReviewedHeadOnce is FR-MERGE-5 and FR-MERGE-12 on the worker: at review_approves,
// once the review passes, the worker that opened the pull request merges it at the reviewed head with the
// repository's merge method, as one recorded effect, and the merge it reports ends the work. Later passes
// merge nothing again.
func TestAWorkerMergesAtTheReviewedHeadOnce(t *testing.T) {
	t.Parallel()
	f := newForgeRig(t, func(forge *fake.Forge, _ *fake.Worktrees) { forge.Push(widgets, "lane/lane-1", "fake-head") })
	level, method := ledger.ReviewApproves, ledger.MergeSquash
	if _, err := control.NewRepositories(f.store).Change(t.Context(), scope, "repo-1", control.RepositoryChange{MergeAuthority: &level, MergeMethod: &method}, member); err != nil {
		t.Fatal(err)
	}
	f.converge(3)
	f.passReview()
	if f.row().State != workorders.Integrating {
		t.Fatalf("the reviewed work reads %s", f.row().State)
	}
	f.converge(3)
	if f.row().State != workorders.Done {
		t.Fatalf("after the merge the work reads %s", f.row().State)
	}
	number := f.forge.PullRequests()[0].Number
	if got := f.forge.MergedWith(number); got != ledger.MergeSquash {
		t.Errorf("the forge merged with %q", got)
	}
	f.clock.Advance(2 * time.Minute)
	f.converge(3)
	merges := 0
	for _, call := range f.forge.Calls() {
		if strings.HasPrefix(call, "merge ") {
			merges++
		}
	}
	if merges != 1 {
		t.Errorf("the worker merged %d times: %v", merges, f.forge.Calls())
	}
	pr, err := f.pullRequest()
	if err != nil || pr.State != ledger.PullRequestMerged || pr.HeadCommit != "fake-head" {
		t.Errorf("the ledger holds %+v: %v", pr, err)
	}
}

// TestAWorkerMergesAReviewedBranchWithGit is FR-MERGE-11 on the worker: with review target `branch` at
// review_approves, the worker that ran the lane merges the reviewed branch into its base with git, with no
// forge, as one recorded effect, and the merge it reports ends the work.
func TestAWorkerMergesAReviewedBranchWithGit(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	repositories := control.NewRepositories(base.store)
	if _, err := repositories.Detect(t.Context(), scope, control.DetectedRepository{ID: "repo-1", DefaultBranch: "main"}, member); err != nil {
		t.Fatal(err)
	}
	level := ledger.ReviewApproves
	if _, err := repositories.Change(t.Context(), scope, "repo-1", control.RepositoryChange{MergeAuthority: &level}, member); err != nil {
		t.Fatal(err)
	}
	worktrees := fake.NewWorktrees()
	base.worktrees, base.repository = worktrees, "repo-1"
	r := newLaneRigFor(t, base)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	f := &forgeRig{laneRig: r, worktrees: worktrees}
	f.converge(2)
	f.passReview()
	if f.row().State != workorders.Integrating {
		t.Fatalf("the reviewed work reads %s", f.row().State)
	}
	f.converge(3)
	if f.row().State != workorders.Done {
		t.Fatalf("after the merge the work reads %s", f.row().State)
	}
	merges := worktrees.Merges()
	if len(merges) != 1 || merges[0].Base != "main" || merges[0].Branch != "lane/lane-1" || merges[0].Method != ledger.MergeCommit || merges[0].WorkOrder != f.id {
		t.Fatalf("the worker merged %+v", merges)
	}
	f.clock.Advance(2 * time.Minute)
	f.converge(2)
	if again := worktrees.Merges(); len(again) != 1 {
		t.Errorf("the worker merged %d times", len(again))
	}
}
