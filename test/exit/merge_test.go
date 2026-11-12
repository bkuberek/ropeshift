package exit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/doubles/ghstate"
	"ropeshift.com/ropeshift/test/gittest"
)

// mergeSource is a checkout with one contract, whose origin is a bare repository on this machine, so a push
// reaches no real remote.
func mergeSource(t *testing.T) (*gittest.Repo, string) {
	t.Helper()
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Commit("first.md", contract.ContractText("first", "First", "Build the first."))
	bare := filepath.Join(t.TempDir(), "widgets.git")
	repo.Git("init", "--quiet", "--bare", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "--quiet", "origin", "main")
	return repo, bare
}

// mergingInstall starts the daemon on the checkout, sets its one repository with these flags, and returns the
// work order of its contract and the repository.
func (in *install) mergingInstall(t *testing.T, repo *gittest.Repo, set ...string) (string, string) {
	t.Helper()
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--forge-tick", "100ms", "--source", repo.Dir)
	t.Cleanup(d.stop)
	t.Cleanup(func() {
		if t.Failed() {
			logged := d.stderr.String()
			t.Logf("the daemon said:\n%s", logged[max(0, len(logged)-6000):])
		}
	})
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	first := in.workOrders(1)["The work order of first"]
	var list api.RepositoryList
	if err := json.Unmarshal([]byte(in.must("repository", "list", "--json")), &list); err != nil || len(list.Repositories) != 1 {
		t.Fatalf("repositories %+v: %v", list, err)
	}
	repository := list.Repositories[0].ID
	in.must(append([]string{"repository", "set", repository[:8]}, set...)...)
	in.must("work", "review", first)
	return first, repository
}

// throughPR waits for the worker's gh sign-in on record, which a pull request needs.
func (in *install) throughPR(repository string) {
	in.t.Helper()
	in.waitFor("the worker's gh sign-in on record", func() bool {
		return strings.Contains(in.must("repository", "show", repository[:8]), "forge\tsolo\tok")
	})
}

// openedAt runs the change lane and waits for its pull request open at the lane's commit.
func (in *install) openedAt(gh, order, claim, lane string, repo *gittest.Repo) (ghstate.PullRequest, string) {
	in.t.Helper()
	in.startLane(order, claim, lane)
	in.waitFor("the lane's outcome", func() bool { return in.show(order).State == workorders.Acceptance })
	head := repo.Git("rev-parse", "refs/heads/lane/lane-1")
	in.waitFor("the pull request at the lane's commit", func() bool {
		pr, ok := pullRequestIn(in.t, gh)
		return ok && pr.State == "OPEN" && repo.RemoteHead("origin", "refs/heads/lane/lane-1") == head
	})
	pr, _ := pullRequestIn(in.t, gh)
	return pr, head
}

// checksAre sets what CI reports on the pull request, the checks its base requires, and waits for the ledger
// to have read them at a head.
func (in *install) checksAre(gh, order string, number int64, head string, required []string, checks ...ghstate.Check) {
	in.t.Helper()
	doubles.EditFakeGH(in.t, gh, func(s *ghstate.State) {
		pr := s.Find(number)
		pr.Checks, pr.Required = checks, required
	})
	in.waitFor("the ledger to read the checks", func() bool {
		pr := in.detail(order).PullRequest
		if pr == nil || pr.HeadCommit != head || len(pr.Checks) != len(checks) {
			return false
		}
		for i, check := range checks {
			if pr.Checks[i].Name != check.Name || pr.Checks[i].Required != slices.Contains(required, check.Name) || string(pr.Checks[i].State) != stateOfCheck(check) {
				return false
			}
		}
		return true
	})
}

func stateOfCheck(check ghstate.Check) string {
	switch {
	case check.Status != "COMPLETED":
		return string(ledger.CheckPending)
	case check.Conclusion == "SUCCESS":
		return string(ledger.CheckPassed)
	}
	return string(ledger.CheckFailed)
}

func ciResult(status, conclusion string) ghstate.Check {
	return ghstate.Check{Typename: "CheckRun", Name: "CI result", Status: status, Conclusion: conclusion, DetailsURL: "https://ci.example/1"}
}

// mergeCalls lists the merges the worker's gh ran.
func mergeCalls(t *testing.T, gh string) [][]string {
	t.Helper()
	var out [][]string
	for _, call := range doubles.FakeGHCalls(t, gh) {
		if len(call.Args) >= 2 && call.Args[0] == "pr" && call.Args[1] == "merge" {
			out = append(out, call.Args)
		}
	}
	return out
}

// TestRopeshiftMergesThePullRequestTheReviewApproved is B3b's exit at review_approves on the real binary,
// with real git, the fake claude, the fake gh, and a bare repository as the remote. A check the base
// requires fails on the first commit, and the work goes back for rework, naming it. On the rework's commit
// the review passes while the check still runs, and the work waits in integrating, saying so. Once the check
// is green, the worker merges the pull request at the reviewed head with the repository's merge method, the
// work reaches done, and the approval by the review lane and the merged commit are on record.
func TestRopeshiftMergesThePullRequestTheReviewApproved(t *testing.T) {
	in := newInstall(t)
	in.aLaneThatCommits()
	gh := in.aForge()
	repo, bare := mergeSource(t)
	first, repository := in.mergingInstall(t, repo, "--review-target", "pull_request", "--forge-binding", forgeBinding,
		"--merge-authority", "review_approves", "--merge-method", "squash")
	in.throughPR(repository)

	// A check the base requires fails on the first commit, and the review's pass does not merge it.
	pr, firstHead := in.openedAt(gh, first, "claim-1", "lane-1", repo)
	in.checksAre(gh, first, pr.Number, firstHead, []string{"CI result"}, ciResult("COMPLETED", "FAILURE"))
	in.judgeThroughPR(first, "claim-2", "review-1", reviewSays("pass", passedReason, "work.txt:1"))
	in.waitFor("the work to go back for its required check", func() bool { return in.show(first).State == workorders.Ready })
	if sent := in.eventsOf("work_order.rework", first); len(sent) != 1 || sent[0].ActorID != "integration" {
		t.Errorf("the rework on record: %+v", sent)
	}
	if calls := mergeCalls(t, gh); len(calls) != 0 {
		t.Fatalf("a red required check merged: %q", calls)
	}

	// The rework's commit moves the same pull request; the review passes while the check still runs.
	in.playing(commitsAFile)
	in.startLane(first, "claim-3", "lane-2")
	in.waitFor("the rework's commit on the pull request", func() bool {
		return repo.RemoteHead("origin", "refs/heads/lane/lane-1") != firstHead && in.acceptance(first).State == workorders.Acceptance
	})
	if prompts := in.firstPrompts(t); !strings.Contains(prompts, "required checks failed on "+firstHead[:8]+": CI result") {
		t.Errorf("the rework was not told which required check failed:\n%s", prompts)
	}
	head := repo.RemoteHead("origin", "refs/heads/lane/lane-1")
	in.checksAre(gh, first, pr.Number, head, []string{"CI result"}, ciResult("IN_PROGRESS", ""))
	in.judgeThroughPR(first, "claim-4", "review-2", reviewSays("pass", passedReason, "work.txt:2"))
	in.waitFor("the work to wait for its check", func() bool {
		return in.show(first).State == workorders.Integrating && strings.Contains(in.must("work", "show", first), "CI result")
	})
	if text := in.must("work", "show", first); !strings.Contains(text, "waiting\tto merge: required checks are still running") {
		t.Errorf("work show does not say what the merge waits on:\n%s", text)
	}
	if calls := mergeCalls(t, gh); len(calls) != 0 {
		t.Fatalf("a pending required check merged: %q", calls)
	}

	// Green, the worker merges at the reviewed head with the method, and the work is done.
	before := repo.Git("--git-dir", bare, "rev-parse", "refs/heads/main")
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) { s.Find(pr.Number).Checks = []ghstate.Check{ciResult("COMPLETED", "SUCCESS")} })
	in.waitFor("the merge to end the work", func() bool { return in.show(first).State == workorders.Done })
	want := []string{"pr", "merge", strconv.FormatInt(pr.Number, 10), "--repo", forgeBinding, "--match-head-commit", head, "--squash"}
	if calls := mergeCalls(t, gh); len(calls) != 1 || !slices.Equal(calls[0], want) {
		t.Fatalf("the worker's gh merged %q, want once %q", calls, want)
	}
	merged, _ := pullRequestIn(t, gh)
	if merged.State != "MERGED" || merged.MergeMethod != "squash" || merged.Head != head {
		t.Errorf("the forge holds %+v", merged)
	}
	if main := repo.Git("--git-dir", bare, "rev-parse", "refs/heads/main"); main == before || main != merged.MergeCommit {
		t.Errorf("the remote's main is %s, want the merge %s", main, merged.MergeCommit)
	}
	// The review approved the first commit too, which never merged, because its required check failed.
	view := in.acceptance(first)
	if len(view.Integrations) != 2 || view.Integrations[0].ApprovedCommit != firstHead || view.Integrations[0].Merged() {
		t.Fatalf("the approvals read %+v", view.Integrations)
	}
	record := view.Integrations[1]
	if record.ApprovedByKind != ledger.ApprovedByReview || record.ApprovedCommit != head || record.MergedCommit != merged.MergeCommit ||
		record.MergeAuthority != ledger.ReviewApproves || record.PullRequest != pr.Number || !strings.HasPrefix(record.ApprovedByID, "review-2") {
		t.Errorf("the approval reads %+v", record)
	}
	if events := in.eventsOf(ledger.EventIntegrationMerged, first); len(events) != 1 || events[0].SubjectKind != ledger.SubjectIntegration {
		t.Errorf("the merge on record: %+v", events)
	}
}

// TestAMemberApprovesTheMergeOfAPullRequest is B3b's exit at person_approves. The review passes, and the
// merge waits for a member's approval bound to the reviewed commit. Someone pushes to the pull request's branch
// before the member approves, so the head is not the reviewed commit: the approval of the old head is void, and
// the new head goes back to review (ADR-0020). The review passes the new head, and the member's approval of
// that commit merges it.
func TestAMemberApprovesTheMergeOfAPullRequest(t *testing.T) {
	in := newInstall(t)
	in.aLaneThatCommits()
	gh := in.aForge()
	repo, _ := mergeSource(t)
	first, repository := in.mergingInstall(t, repo, "--review-target", "pull_request", "--forge-binding", forgeBinding, "--merge-authority", "person_approves")
	in.throughPR(repository)
	pr, head := in.openedAt(gh, first, "claim-1", "lane-1", repo)
	in.judgeThroughPR(first, "claim-2", "review-1", reviewSays("pass", passedReason, "work.txt:1"))
	in.waitFor("the work to wait for its merge", func() bool { return in.show(first).State == workorders.Integrating })
	approve := in.mergeRequest(first, authority.MergeApprove)
	if approve.SubjectDigest != head || !strings.Contains(approve.Summary, "#"+strconv.FormatInt(pr.Number, 10)) {
		t.Fatalf("the merge request reads %+v", approve)
	}
	if text := in.must("approval", "show", approve.ID); !strings.Contains(text, "merge: work order "+first) || !strings.Contains(text, "person_approves") {
		t.Errorf("approval show reads:\n%s", text)
	}

	// Someone else's push moves the head: the old head's approval is void, and the new head is reviewed again.
	tree := repo.Git("rev-parse", head+"^{tree}")
	moved := repo.Git("commit-tree", tree, "-p", head, "-m", "someone else's push")
	repo.Git("push", "--quiet", "origin", moved+":refs/heads/lane/lane-1")
	in.waitFor("the moved head to go back to review", func() bool {
		view := in.acceptance(first)
		return view.State == workorders.Acceptance && len(view.Reviews) == 2
	})
	if code, _, stderr := in.run("approval", "approve", approve.ID, "--digest", head); code == 0 {
		t.Fatalf("the approval of the old head still decided: %s", stderr)
	}
	if moves := in.eventsOf("work_order.head_moved", first); len(moves) != 1 {
		t.Errorf("the move back to review on record: %+v", moves)
	}
	if calls := mergeCalls(t, gh); len(calls) != 0 {
		t.Fatalf("a head no review judged merged: %q", calls)
	}

	// The new head passes its review, and the member's approval of it merges it.
	in.judgeThroughPR(first, "claim-3", "review-2", reviewSays("pass", passedReason, "work.txt:1"))
	in.waitFor("the work to wait for its merge again", func() bool { return in.show(first).State == workorders.Integrating })
	again := in.mergeRequest(first, authority.MergeApprove)
	if again.SubjectDigest != moved {
		t.Fatalf("the new merge request binds to %s, want %s", again.SubjectDigest, moved)
	}
	in.must("approval", "approve", again.ID, "--digest", moved)
	in.waitFor("the merge to end the work", func() bool { return in.show(first).State == workorders.Done })
	if calls := mergeCalls(t, gh); len(calls) != 1 || !slices.Contains(calls[0], moved) || !slices.Contains(calls[0], "--merge") {
		t.Fatalf("the worker's gh merged %q", calls)
	}
	view := in.acceptance(first)
	if len(view.Integrations) != 1 || view.Integrations[0].ApprovedByKind != ledger.ApprovedByMember || view.Integrations[0].ApprovedCommit != moved || !view.Integrations[0].Merged() {
		t.Errorf("the approvals read %+v", view.Integrations)
	}
}

// mergeRequest waits for the one open merge request of a subject for a work order.
func (in *install) mergeRequest(order, subject string) authority.Approval {
	in.t.Helper()
	var found authority.Approval
	in.waitFor("a merge request asking "+subject, func() bool {
		var list api.ApprovalList
		if err := json.Unmarshal([]byte(in.must("approval", "list", "--json", "--state", "pending")), &list); err != nil {
			in.t.Fatal(err)
		}
		for _, row := range list.Approvals {
			if row.Kind == authority.KindMerge && row.WorkOrder == order && row.Subject == subject {
				found = row
				return true
			}
		}
		return false
	})
	return found
}

// mergesTheBase is a rework lane that merges the current base into its branch, keeping its own side of each
// conflict, and commits the merge, as the rework's brief asks.
const mergesTheBase = `{
  "source": "a rework lane that merges the base into its branch",
  "turns": [[
    {"init": true},
    {"tool": "Bash", "command": "git fetch -q origin main && git merge -q -X ours --no-edit FETCH_HEAD"},
    {"mcp": "lane_report", "arguments": {"outcome": "completed", "artifacts": ["branch"], "checks": [{"name": "stand-in", "passed": true}]}},
    {"emit": {"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.02, "usage": {"input_tokens": 3, "output_tokens": 5}}}
  ]]
}`

// TestAConflictSendsTheWorkBackOnItsBranch is B3b's conflict path. While the review runs, the base gains a
// change to the file the lane wrote, so the merge would conflict. The worker reads the conflict and merges
// nothing, and the work goes back to rework on the same branch, with the conflict in its brief. The rework
// lane merges the base into the branch, its push moves the same pull request, the review passes again, and
// the merge lands.
func TestAConflictSendsTheWorkBackOnItsBranch(t *testing.T) {
	in := newInstall(t)
	in.aLaneThatCommits()
	gh := in.aForge()
	repo, bare := mergeSource(t)
	first, repository := in.mergingInstall(t, repo, "--review-target", "pull_request", "--forge-binding", forgeBinding, "--merge-authority", "review_approves")
	in.throughPR(repository)
	pr, head := in.openedAt(gh, first, "claim-1", "lane-1", repo)

	// Someone else's clone adds its own work.txt to the base on the remote.
	clone := filepath.Join(t.TempDir(), "clone")
	repo.Git("clone", "--quiet", "--branch", "main", bare, clone)
	if err := os.WriteFile(filepath.Join(clone, "work.txt"), []byte("the base's own work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo.Git("-C", clone, "add", "work.txt")
	repo.Git("-C", clone, "commit", "--quiet", "-m", "the base's work")
	repo.Git("-C", clone, "push", "--quiet", "origin", "main")

	in.judgeThroughPR(first, "claim-2", "review-1", reviewSays("pass", passedReason, "work.txt:1"))
	in.waitFor("the conflict to send the work back", func() bool { return in.show(first).State == workorders.Ready })
	if calls := mergeCalls(t, gh); len(calls) != 0 {
		t.Fatalf("a conflicting pull request merged: %q", calls)
	}
	if branch := in.show(first).Branch; branch != "lane/lane-1" {
		t.Errorf("the rework continues %q", branch)
	}

	// The rework merges the base into the same branch, and the same pull request moves.
	in.playing(mergesTheBase)
	in.startLane(first, "claim-3", "lane-2")
	in.waitFor("the rework's merge on the pull request", func() bool {
		return repo.RemoteHead("origin", "refs/heads/lane/lane-1") != head && in.acceptance(first).State == workorders.Acceptance
	})
	prompts := in.firstPrompts(t)
	for _, want := range []string{"into main conflicts with main", "Merge the current main into this branch"} {
		if !strings.Contains(prompts, want) {
			t.Errorf("the rework was not told %q:\n%s", want, prompts)
		}
	}
	if state := doubles.FakeGH(t, gh); len(state.PullRequests) != 1 {
		t.Fatalf("the rework opened another pull request: %+v", state.PullRequests)
	}
	again := repo.RemoteHead("origin", "refs/heads/lane/lane-1")
	in.judgeThroughPR(first, "claim-4", "review-2", reviewSays("pass", passedReason, "work.txt:1"))
	in.waitFor("the merge to end the work", func() bool { return in.show(first).State == workorders.Done })
	if calls := mergeCalls(t, gh); len(calls) != 1 || !slices.Contains(calls[0], again) || !slices.Contains(calls[0], strconv.FormatInt(pr.Number, 10)) {
		t.Fatalf("the worker's gh merged %q", calls)
	}
}

// TestRopeshiftMergesAReviewedBranchAndPushesTheBase is B3b's exit for review target `branch` at
// review_approves: nothing is pushed before the merge, the worker merges the lane's branch into the base in a
// worktree of its own, never in the checkout, and pushes the base to the remote without force. The checkout's
// own branches stay where they were, and the merge commit names the work order and the reviewed commit.
func TestRopeshiftMergesAReviewedBranchAndPushesTheBase(t *testing.T) {
	in := newInstall(t)
	in.aLaneThatCommits()
	repo, bare := mergeSource(t)
	first, _ := in.mergingInstall(t, repo, "--merge-authority", "review_approves")
	in.startLane(first, "claim-1", "lane-1")
	in.waitFor("the lane's outcome", func() bool { return in.show(first).State == workorders.Acceptance })
	head := repo.Git("rev-parse", "refs/heads/lane/lane-1")
	if remote := repo.RemoteHead("origin", "refs/heads/lane/lane-1"); remote != "" {
		t.Errorf("review target branch pushed the lane's branch: %s", remote)
	}
	localMain := repo.Git("rev-parse", "main")
	remoteMain := repo.Git("--git-dir", bare, "rev-parse", "refs/heads/main")
	in.judge(first, "claim-2", "review-1", reviewSays("pass", passedReason, "work.txt:1"))
	in.waitFor("the merge to end the work", func() bool { return in.show(first).State == workorders.Done })
	merged := repo.Git("--git-dir", bare, "rev-parse", "refs/heads/main")
	if merged == remoteMain {
		t.Fatal("the remote's main did not move")
	}
	parents := strings.Fields(repo.Git("--git-dir", bare, "log", "-1", "--format=%P", merged))
	if len(parents) != 2 || parents[0] != remoteMain || parents[1] != head {
		t.Errorf("the merge's parents are %v, want %s and %s", parents, remoteMain, head)
	}
	if message := repo.Git("--git-dir", bare, "log", "-1", "--format=%B", merged); !strings.Contains(message, "Ropeshift-Work-Order: "+first) || !strings.Contains(message, "Ropeshift-Reviewed-Commit: "+head) {
		t.Errorf("the merge commit reads %q", message)
	}
	if got := repo.Git("rev-parse", "main"); got != localMain {
		t.Errorf("the checkout's main moved from %s to %s", localMain, got)
	}
	if worktrees := repo.Git("worktree", "list", "--porcelain"); strings.Contains(worktrees, "ropeshift-merge-") {
		t.Errorf("the merge left its worktree:\n%s", worktrees)
	}
	view := in.acceptance(first)
	if len(view.Integrations) != 1 || view.Integrations[0].MergedCommit != merged || view.Integrations[0].PullRequest != 0 || view.Integrations[0].ApprovedByKind != ledger.ApprovedByReview {
		t.Errorf("the approvals read %+v", view.Integrations)
	}
	if text := in.must("work", "show", first); !strings.Contains(text, "approved\t"+head[:12]+"\tby review") || !strings.Contains(text, "merged as "+merged[:12]) {
		t.Errorf("work show reads:\n%s", text)
	}
}
