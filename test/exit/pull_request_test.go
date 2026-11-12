package exit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/doubles/ghstate"
	"ropeshift.com/ropeshift/test/gittest"
)

// forgeBinding is where the fake forge says the repository lives. Its remote is a bare repository on this
// machine, which names no forge, so the binding is named.
const forgeBinding = "github.com/acme/widgets"

// detail reads one work order as work show gives it, with its pull request.
func (in *install) detail(id string) api.WorkDetail {
	in.t.Helper()
	var detail api.WorkDetail
	if err := json.Unmarshal([]byte(in.must("work", "show", id, "--json")), &detail); err != nil {
		in.t.Fatal(err)
	}
	return detail
}

// pullRequest reads the one pull request the fake forge holds.
func pullRequestIn(t *testing.T, gh string) (ghstate.PullRequest, bool) {
	t.Helper()
	state := doubles.FakeGH(t, gh)
	if len(state.PullRequests) != 1 {
		return ghstate.PullRequest{}, false
	}
	return state.PullRequests[0], true
}

// judgeThroughPR runs the open review of a work order once its pull request released it, on a lane of its own.
func (in *install) judgeThroughPR(order, claim, lane, fixture string) workorders.WorkOrder {
	in.t.Helper()
	review := in.openReviewOf(order)
	in.waitFor("the pull request to release the review", func() bool { return in.show(review.ID).StartHeld == nil })
	in.playing(fixture)
	in.must("work", "claim", review.ID, "--worker", in.workers()[0].ID, "--claim-id", claim)
	in.must("lane", "start", review.ID, "--lane", lane)
	in.waitFor("the review to be done", func() bool { return in.show(review.ID).State == workorders.Done })
	return review
}

// revOf is the revision of the first event of a type about a subject, or zero.
func (in *install) revOf(eventType, subject string) int64 {
	in.t.Helper()
	for _, event := range in.ledgerEvents() {
		if event.Type == eventType && event.SubjectID == subject {
			return event.Rev
		}
	}
	return 0
}

// TestAChangeIsReviewedThroughItsPullRequest is B3a's exit on the real binary, with real git, the fake
// claude, the fake gh, and a bare repository as the remote: nothing reaches GitHub. A change lane completes,
// and the worker pushes its branch and opens the pull request before the review starts. The review's
// verdicts reach the pull request as a comment, a failed one sends the work to rework, and the rework
// pushes to the same branch, which moves the same pull request. Accepted at person_merges, the work waits in
// integrating, the person merges it in the forge, and the read of that merge ends the work. No lane ever sees a
// forge credential.
func TestAChangeIsReviewedThroughItsPullRequest(t *testing.T) {
	in := newInstall(t)
	in.aLaneThatCommits()
	claude, _, _ := strings.Cut(in.path, string(os.PathListSeparator))
	gh := in.aForge()
	// The person's own sign-in reaches the daemon's gh, and no lane.
	in.daemonEnv = append(in.daemonEnv, "GH_TOKEN=the-persons-token")
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Commit("first.md", contract.ContractText("first", "First", "Build the first."))
	bare := filepath.Join(t.TempDir(), "widgets.git")
	repo.Git("init", "--quiet", "--bare", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "--quiet", "origin", "main")

	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--forge-tick", "100ms", "--source", repo.Dir)
	t.Cleanup(d.stop)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	first := in.workOrders(1)["The work order of first"]
	var list api.RepositoryList
	if err := json.Unmarshal([]byte(in.must("repository", "list", "--json")), &list); err != nil || len(list.Repositories) != 1 {
		t.Fatalf("repositories %+v: %v", list, err)
	}
	repository := list.Repositories[0].ID
	in.must("repository", "set", repository[:8], "--review-target", "pull_request", "--forge-binding", forgeBinding)
	in.waitFor("the worker's gh sign-in on record", func() bool {
		return strings.Contains(in.must("repository", "show", repository[:8]), "forge\tsolo\tok")
	})
	in.must("work", "review", first)

	// A change lane completes, and the worker pushes its branch and opens the pull request.
	in.startLane(first, "claim-1", "lane-1")
	in.waitFor("the first attempt's outcome", func() bool { return in.show(first).State == workorders.Acceptance })
	firstHead := repo.Git("rev-parse", "refs/heads/lane/lane-1")
	in.waitFor("the pull request", func() bool {
		pr, ok := pullRequestIn(t, gh)
		return ok && pr.State == "OPEN"
	})
	pr, _ := pullRequestIn(t, gh)
	if pr.Repo != forgeBinding || pr.Branch != "lane/lane-1" || pr.Base != "main" || !strings.Contains(pr.Body, first) || !strings.Contains(pr.Body, "## Criteria") {
		t.Errorf("the pull request reads %+v", pr)
	}
	if remote := repo.RemoteHead("origin", "refs/heads/lane/lane-1"); remote != firstHead {
		t.Errorf("the remote holds %q for the lane's branch, want %s", remote, firstHead)
	}
	failed := in.judgeThroughPR(first, "claim-2", "review-1", reviewSays("fail", failedReason, "work.txt:1", "FR-LOG-2"))
	opened, started := in.revOf(ledger.EventPullRequestOpened, first), in.revOf(workorders.EventType(workorders.TriggerLaneStarted), failed.ID)
	if opened == 0 || started == 0 || opened > started {
		t.Errorf("the pull request opened at revision %d and the review's lane started at %d", opened, started)
	}

	// The verdicts reach the pull request, and the failed criterion sends the work back.
	in.waitFor("the failed verdict on the pull request", func() bool {
		pr, _ := pullRequestIn(t, gh)
		return len(pr.Comments) == 1
	})
	pr, _ = pullRequestIn(t, gh)
	for _, want := range []string{failed.ID, firstHead[:8], "**fail**", failedReason, "FR-LOG-2", "goes back for rework"} {
		if !strings.Contains(pr.Comments[0], want) {
			t.Errorf("the comment leaves out %q:\n%s", want, pr.Comments[0])
		}
	}
	in.waitFor("the work to go back", func() bool { return in.show(first).State == workorders.Ready })

	// Rework continues the same branch, and its push moves the same pull request to the new head.
	in.playing(commitsAFile)
	in.startLane(first, "claim-3", "lane-2")
	in.waitFor("the second attempt's outcome", func() bool {
		view := in.acceptance(first)
		return view.State == workorders.Acceptance && len(view.Reviews) == 2
	})
	in.waitFor("the new head on the remote", func() bool {
		return repo.RemoteHead("origin", "refs/heads/lane/lane-1") != firstHead
	})
	secondHead := repo.RemoteHead("origin", "refs/heads/lane/lane-1")
	if state := doubles.FakeGH(t, gh); len(state.PullRequests) != 1 {
		t.Fatalf("the rework opened another pull request: %+v", state.PullRequests)
	}
	passed := in.judgeThroughPR(first, "claim-4", "review-2", reviewSays("pass", passedReason, "work.txt:2"))
	in.waitFor("the passing verdict on the pull request", func() bool {
		pr, _ := pullRequestIn(t, gh)
		return len(pr.Comments) == 2
	})
	pr, _ = pullRequestIn(t, gh)
	if !strings.Contains(pr.Comments[1], passed.ID) || !strings.Contains(pr.Comments[1], secondHead[:8]) || !strings.Contains(pr.Comments[1], "you merge this pull request yourself") {
		t.Errorf("the second comment reads:\n%s", pr.Comments[1])
	}
	view := in.acceptance(first)
	if view.State != workorders.Integrating || len(view.Verdicts) != 1 || view.Verdicts[0].Commit != secondHead || view.Verdicts[0].Review != passed.ID {
		t.Fatalf("at person_merges the work reads %+v", view)
	}
	if detail := in.detail(first); detail.PullRequest == nil || detail.PullRequest.Number != pr.Number || detail.PullRequest.HeadCommit != secondHead {
		t.Errorf("work show names the pull request %+v", detail.PullRequest)
	}

	// The person merges the pull request in the forge, and the integration tick ends the work.
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) {
		merged := s.Find(pr.Number)
		merged.State, merged.Head, merged.MergeCommit, merged.MergedAt = "MERGED", secondHead, strings.Repeat("e", 40), "2026-11-11T12:00:00Z"
	})
	in.waitFor("the merge to end the work", func() bool { return in.show(first).State == workorders.Done })
	if merges := in.eventsAppear(ledger.EventPullRequestMerged, first, 1); len(merges) != 1 || merges[0].ActorKind != "worker" {
		t.Errorf("the merge on record: %+v", merges)
	}
	if ended := in.eventsAppear("work_order.merged", first, 1); len(ended) != 1 || ended[0].ActorID != "forge" {
		t.Errorf("the merge that ended the work on record: %+v", ended)
	}

	// The worker ran gh under the person's sign-in, with the commands the adapter runs, and never signed in.
	for _, call := range doubles.FakeGHCalls(t, gh) {
		if call.Env["GH_TOKEN"] != "the-persons-token" {
			t.Errorf("the worker's gh ran without the person's sign-in: %v", call.Args)
		}
		if slices.Contains(call.Args, "login") || slices.Contains(call.Args, "token") {
			t.Errorf("the worker ran gh %q", call.Args)
		}
	}
	// No lane saw a forge credential, and each read gh's configuration from an empty directory of its own.
	seen := doubles.SeenBy(t, claude)
	if len(seen) == 0 {
		t.Fatal("no lane session was seen")
	}
	for _, session := range seen {
		for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
			if _, ok := session.Env[name]; ok {
				t.Errorf("%s reached a lane", name)
			}
		}
		config := session.Env["GH_CONFIG_DIR"]
		if config == "" || !strings.HasPrefix(config, in.paths().StateDir) {
			t.Errorf("a lane's GH_CONFIG_DIR is %q", config)
		}
	}
}
