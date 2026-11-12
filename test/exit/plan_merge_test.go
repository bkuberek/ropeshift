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
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/doubles/ghstate"
	"ropeshift.com/ropeshift/test/gittest"
)

// The plan a planning lane writes: a plan, and one contract under it with one work order.
const (
	mergedPlan = "+++\nkind = \"plan\"\nid = \"retry-plan\"\ntitle = \"Retry budget\"\n+++\n\nAdd a retry budget.\n"
	budgetText = "+++\nkind = \"contract\"\nid = \"budget\"\ntitle = \"The retry budget\"\nplan = \"retry-plan\"\n\n" +
		"[[criteria]]\nid = \"c1\"\ntext = \"A failed call retries within a budget\"\n\n" +
		"[[work_orders]]\nid = \"budget-work\"\ntitle = \"Add the retry budget\"\nrepo = \".\"\nsize = \"small\"\n+++\n\n"
	budgetBody = "Retry a failed call within a budget.\n"
)

// writesThePlan is a planning lane that writes the plan's files and commits them on its branch.
func writesThePlan(t *testing.T) string {
	t.Helper()
	staged := filepath.Join(t.TempDir(), "plans")
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"plan.md": mergedPlan, "budget.md": budgetText + budgetBody} {
		if err := os.WriteFile(filepath.Join(staged, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commit := "cp -R " + staged + " . && git add plans && git -c user.name=planner -c user.email=planner@example.com commit -q -m 'Plan the retry budget'"
	fixture, err := json.Marshal(map[string]any{
		"source": "a planning lane that writes a plan and its contract",
		"turns": [][]map[string]any{{
			{"init": true},
			{"hook": "UserPromptSubmit", "input": map[string]any{"prompt": "the brief"}},
			{"tool": "Bash", "command": commit},
			{"mcp": "lane_report", "arguments": map[string]any{"outcome": "completed", "checks": []map[string]any{{"name": "wrote the plan", "passed": true, "criterion": "c1"}}}},
			{"emit": map[string]any{"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.02, "usage": map[string]any{"input_tokens": 3, "output_tokens": 5}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(fixture)
}

// planningInstall starts the daemon on a checkout whose origin is a bare repository on this machine, so a push
// reaches no real remote, with a planning lane that writes a plan, and with the fake gh when withGH says so. It
// sets the checkout's repository with these flags, and returns the checkout, the bare remote, the repository, and
// the fake gh's directory.
func (in *install) planningInstall(t *testing.T, withGH bool, set ...string) (*gittest.Repo, string, string, string) {
	t.Helper()
	in.path = doubles.InstallFakeClaude(t, "planning-lane")
	in.playing(writesThePlan(t))
	if err := os.WriteFile(filepath.Join(in.home, ".gitconfig"), []byte("[user]\n\tname = Lane\n\temail = lane@example.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gh := ""
	if withGH {
		gh = in.aForge()
	}
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	bare := filepath.Join(t.TempDir(), "widgets.git")
	repo.Git("init", "--quiet", "--bare", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "--quiet", "origin", "main")
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--forge-tick", "100ms", "--source", repo.Dir)
	t.Cleanup(d.stop)
	t.Cleanup(func() {
		if t.Failed() {
			_, out, errText := in.run("approval", "list")
			t.Logf("approval list:\n%s%s", out, errText)
			logged := d.stderr.String()
			t.Logf("the daemon said:\n%s", logged[max(0, len(logged)-40000):])
		}
	})
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	var list api.RepositoryList
	in.waitFor("the repository", func() bool {
		if err := json.Unmarshal([]byte(in.must("repository", "list", "--json")), &list); err != nil {
			t.Fatal(err)
		}
		return len(list.Repositories) == 1
	})
	repository := list.Repositories[0].ID
	if len(set) != 0 {
		in.must(append([]string{"repository", "set", repository[:8]}, set...)...)
	}
	return repo, bare, repository, gh
}

// planLaneEnds runs the planning lane of a new `plan` work order, and returns the commit its branch holds.
func (in *install) planLaneEnds(repo *gittest.Repo, repository string) string {
	in.t.Helper()
	in.must("work", "create", "--id", "plan-1", "--kind", "plan", "--repository", repository,
		"--title", "Plan the retry budget", "--statement", "Write the plan for a retry budget.", "--criterion", "The plan is on the branch.")
	in.must("work", "review", "plan-1")
	in.startLane("plan-1", "claim-1", "lane-1")
	in.waitFor("the planning lane's end", func() bool { return in.show("plan-1").State == workorders.Acceptance })
	head := repo.Git("rev-parse", "refs/heads/lane/lane-1")
	if files := repo.Git("ls-tree", "-r", "--name-only", head); !strings.Contains(files, "plans/plan.md") {
		in.t.Fatalf("the planning lane's commit %s holds %q", head, files)
	}
	return head
}

// planApprovalAt waits for the one pending request to approve the plan a branch holds at a commit.
func (in *install) planApprovalAt(commit string) authority.Approval {
	in.t.Helper()
	var found authority.Approval
	in.waitFor("the plan's approval request at "+commit[:8], func() bool {
		var list api.ApprovalList
		if err := json.Unmarshal([]byte(in.must("approval", "list", "--json", "--state", "pending")), &list); err != nil {
			in.t.Fatal(err)
		}
		for _, row := range list.Approvals {
			var detail planning.PlanDetail
			_ = json.Unmarshal([]byte(row.Detail), &detail)
			if row.Kind == authority.KindPlan && detail.Source == commit {
				found = row
				return true
			}
		}
		return false
	})
	return found
}

// TestAnApprovedPlanIsMergedThroughItsPullRequest is B3c's exit at review_approves through a pull request, on the
// real binary with real git, the fake claude, the fake gh, and a bare repository as the remote. The planning
// lane's end pushes its branch and opens its pull request, which says a plan approval is its review. Nothing
// merges before the plan is approved. The member's plan approval accepts the planning work, intake records the
// plan at the approved commit, and the worker merges the pull request at that commit with the repository's
// method. The approval on record is the member's, naming the plan version.
func TestAnApprovedPlanIsMergedThroughItsPullRequest(t *testing.T) {
	in := newInstall(t)
	repo, bare, repository, gh := in.planningInstall(t, true, "--review-target", "pull_request", "--forge-binding", forgeBinding, "--merge-authority", "review_approves")
	in.throughPR(repository)
	head := in.planLaneEnds(repo, repository)
	in.waitFor("the planning branch's pull request", func() bool {
		pr, ok := pullRequestIn(t, gh)
		return ok && pr.State == "OPEN" && repo.RemoteHead("origin", "refs/heads/lane/lane-1") == head
	})
	pr, _ := pullRequestIn(t, gh)
	if pr.Branch != "lane/lane-1" || pr.Base != "main" || !strings.Contains(pr.Body, "plan-1") || !strings.Contains(pr.Body, "plan approval") {
		t.Errorf("the planning branch's pull request reads %+v", pr)
	}

	approval := in.planApprovalAt(head)
	if calls := mergeCalls(t, gh); len(calls) != 0 {
		t.Fatalf("a plan nobody approved merged: %q", calls)
	}
	before := repo.Git("--git-dir", bare, "rev-parse", "refs/heads/main")
	in.must("approval", "approve", approval.ID, "--digest", approval.SubjectDigest)
	in.waitFor("the merge to end the planning work", func() bool { return in.show("plan-1").State == workorders.Done })
	want := []string{"pr", "merge", strconv.FormatInt(pr.Number, 10), "--repo", forgeBinding, "--match-head-commit", head, "--merge"}
	if calls := mergeCalls(t, gh); len(calls) != 1 || !slices.Equal(calls[0], want) {
		t.Fatalf("the worker's gh merged %q, want once %q", calls, want)
	}
	merged, _ := pullRequestIn(t, gh)
	if main := repo.Git("--git-dir", bare, "rev-parse", "refs/heads/main"); main == before || main != merged.MergeCommit {
		t.Errorf("the remote's main is %s, want the merge %s", main, merged.MergeCommit)
	}
	view := in.acceptance("plan-1")
	if len(view.Integrations) != 1 {
		t.Fatalf("the approvals read %+v", view.Integrations)
	}
	record := view.Integrations[0]
	if record.ApprovedByKind != ledger.ApprovedByMember || record.ApprovedCommit != head || record.MergedCommit != merged.MergeCommit ||
		record.MergeAuthority != ledger.ReviewApproves || record.PullRequest != pr.Number || record.Plan == "" || record.PlanVersion != 1 {
		t.Errorf("the approval reads %+v", record)
	}
	if events := in.eventsAppear(ledger.EventIntegrationMerged, "plan-1", 1); len(events) != 1 {
		t.Errorf("the merge on record: %+v", events)
	}
	if text := in.must("work", "show", "plan-1"); !strings.Contains(text, "plan "+record.Plan+" version 1") {
		t.Errorf("work show does not name the plan version:\n%s", text)
	}
	if text := in.must("work", "list"); !strings.Contains(text, "Add the retry budget") {
		t.Errorf("the approved plan's work is not on record:\n%s", text)
	}
}

// TestAtPersonMergesAnApprovedPlanIsNotMerged is B3c's exit at person_merges with review target branch: the
// plan approval ends the planning and records the plan, and nothing merges; the person merges the branch when
// they choose.
func TestAtPersonMergesAnApprovedPlanIsNotMerged(t *testing.T) {
	in := newInstall(t)
	repo, bare, repository, _ := in.planningInstall(t, false)
	head := in.planLaneEnds(repo, repository)
	localMain, remoteMain := repo.Git("rev-parse", "main"), repo.Git("--git-dir", bare, "rev-parse", "refs/heads/main")
	approval := in.planApprovalAt(head)
	in.must("approval", "approve", approval.ID, "--digest", approval.SubjectDigest)
	in.waitFor("intake to record the approved plan", func() bool {
		return in.show("plan-1").PlanCommit == head && strings.Contains(in.must("work", "list"), "Add the retry budget")
	})
	if state := in.show("plan-1").State; state != workorders.Done {
		t.Errorf("at person_merges the approved planning work is %s", state)
	}
	if view := in.acceptance("plan-1"); len(view.Integrations) != 0 {
		t.Errorf("at person_merges approvals for merge are on record: %+v", view.Integrations)
	}
	if now := repo.Git("rev-parse", "main"); now != localMain {
		t.Errorf("the checkout's main moved from %s to %s", localMain, now)
	}
	if now := repo.Git("--git-dir", bare, "rev-parse", "refs/heads/main"); now != remoteMain {
		t.Errorf("the remote's main moved from %s to %s", remoteMain, now)
	}
	if remote := repo.RemoteHead("origin", "refs/heads/lane/lane-1"); remote != "" {
		t.Errorf("review target branch pushed the planning branch: %s", remote)
	}
}

// TestAMovedPlanningBranchIsNotMergedAtTheApprovedCommit is B3c's moved head: the plan is approved while a check
// the base requires still runs, so the merge waits. Someone pushes a new plan to the pull request's branch, and the
// approval of the old commit merges nothing: the planning work goes back to acceptance at the new head, whose plan
// asks for its own approval, and only that approval merges the new head.
func TestAMovedPlanningBranchIsNotMergedAtTheApprovedCommit(t *testing.T) {
	in := newInstall(t)
	repo, bare, repository, gh := in.planningInstall(t, true, "--review-target", "pull_request", "--forge-binding", forgeBinding, "--merge-authority", "review_approves")
	in.throughPR(repository)
	head := in.planLaneEnds(repo, repository)
	in.waitFor("the planning branch's pull request", func() bool {
		pr, ok := pullRequestIn(t, gh)
		return ok && pr.State == "OPEN" && repo.RemoteHead("origin", "refs/heads/lane/lane-1") == head
	})
	pr, _ := pullRequestIn(t, gh)
	in.checksAre(gh, "plan-1", pr.Number, head, []string{"CI result"}, ciResult("IN_PROGRESS", ""))
	first := in.planApprovalAt(head)
	in.must("approval", "approve", first.ID, "--digest", first.SubjectDigest)
	in.waitFor("the approved plan to wait for its check", func() bool {
		return in.show("plan-1").State == workorders.Integrating && len(in.acceptance("plan-1").Integrations) == 1
	})

	// Someone else's clone pushes a new plan to the pull request's branch. The checkout holds the commit too, as a
	// fetch leaves it, so intake can read it.
	clone := filepath.Join(t.TempDir(), "clone")
	repo.Git("clone", "--quiet", "--branch", "lane/lane-1", bare, clone)
	if err := os.WriteFile(filepath.Join(clone, "plans", "budget.md"), []byte(budgetText+"Retry a failed call within a budget of three.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo.Git("-C", clone, "commit", "--quiet", "-am", "a budget of three")
	moved := repo.Git("-C", clone, "rev-parse", "HEAD")
	repo.Git("fetch", "--quiet", clone, "lane/lane-1")
	repo.Git("-C", clone, "push", "--quiet", "origin", "lane/lane-1")
	in.waitFor("the moved head to go back to acceptance", func() bool {
		view := in.acceptance("plan-1")
		branches := branchesOf(view)
		return view.State == workorders.Acceptance && len(branches) != 0 && branches[len(branches)-1].Digest == moved
	})
	if moves := in.eventsAppear("work_order.head_moved", "plan-1", 1); len(moves) != 1 {
		t.Errorf("the move back to acceptance on record: %+v", moves)
	}
	if calls := mergeCalls(t, gh); len(calls) != 0 {
		t.Fatalf("the approval of the old head merged: %q", calls)
	}

	// The new head's plan asks for its own approval, which merges it once its check is green.
	second := in.planApprovalAt(moved)
	if second.ID == first.ID || second.SubjectDigest == first.SubjectDigest {
		t.Fatalf("the new head asks %+v", second)
	}
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) { s.Find(pr.Number).Checks = []ghstate.Check{ciResult("COMPLETED", "SUCCESS")} })
	in.must("approval", "approve", second.ID, "--digest", second.SubjectDigest)
	in.waitFor("the merge to end the planning work", func() bool { return in.show("plan-1").State == workorders.Done })
	if calls := mergeCalls(t, gh); len(calls) != 1 || !slices.Contains(calls[0], moved) {
		t.Fatalf("the worker's gh merged %q, want once at %s", calls, moved)
	}
	view := in.acceptance("plan-1")
	if len(view.Integrations) != 2 || view.Integrations[0].ApprovedCommit != head || view.Integrations[0].Merged() ||
		view.Integrations[1].ApprovedCommit != moved || !view.Integrations[1].Merged() || view.Integrations[1].PlanVersion != 2 {
		t.Errorf("the approvals read %+v", view.Integrations)
	}
}

// TestAnApprovedAdoptionBranchIsMerged is B3c's adoption: `ropeshift adopt` in a repository whose origin is a bare
// repository on this machine, at review_approves with review target branch. Nothing reaches the default branch
// before the person approves the plan; the approval merges the adoption branch at the approved commit into the
// remote's main, in a worktree of the worker's own, and the checkout's main stays where it was.
func TestAnApprovedAdoptionBranchIsMerged(t *testing.T) {
	in := newInstall(t)
	repo := shopRepository(t)
	bare := filepath.Join(t.TempDir(), "shop.git")
	repo.Git("init", "--quiet", "--bare", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "--quiet", "origin", "main")
	in.path = doubles.InstallFakeClaude(t, "planning-lane")
	in.playing(adoptLane(t))
	if err := os.WriteFile(filepath.Join(in.home, ".gitconfig"), []byte("[user]\n\tname = Lane\n\temail = lane@example.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms")
	t.Cleanup(d.stop)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	code, stdout, stderr := in.runIn(repo.Dir, "adopt", "--json")
	if code != cli.ExitOK {
		t.Fatalf("adopt: exit %d: %s%s", code, stdout, stderr)
	}
	var started struct {
		WorkOrder string `json:"work_order"`
		Lane      string `json:"lane"`
	}
	if err := json.Unmarshal([]byte(stdout), &started); err != nil || started.Lane == "" {
		t.Fatalf("adopt printed %q: %v", stdout, err)
	}
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", started.Lane))
	var list api.RepositoryList
	if err := json.Unmarshal([]byte(in.must("repository", "list", "--json", "--project", "shop")), &list); err != nil || len(list.Repositories) != 1 {
		t.Fatalf("repositories %+v: %v", list, err)
	}
	in.must("repository", "set", list.Repositories[0].ID[:8], "--merge-authority", "review_approves", "--project", "shop")
	in.waitFor("the adopt lane's end", func() bool { return in.showIn("shop", started.WorkOrder).State == workorders.Acceptance })

	localMain, remoteMain := repo.Git("rev-parse", "main"), repo.Git("--git-dir", bare, "rev-parse", "refs/heads/main")
	approval := in.awaitPlanApproval("shop")
	head := branchesOf(in.acceptanceIn("shop", started.WorkOrder))[0].Digest
	if now := repo.Git("--git-dir", bare, "rev-parse", "refs/heads/main"); now != remoteMain {
		t.Fatalf("the remote's main moved before the plan was approved: %s", now)
	}
	in.must("approval", "approve", approval.ID, "--digest", approval.SubjectDigest, "--project", "shop")
	in.waitFor("the merge to end the adopt session's work", func() bool { return in.showIn("shop", started.WorkOrder).State == workorders.Done })
	merged := repo.Git("--git-dir", bare, "rev-parse", "refs/heads/main")
	parents := strings.Fields(repo.Git("--git-dir", bare, "log", "-1", "--format=%P", merged))
	if len(parents) != 2 || parents[0] != remoteMain || parents[1] != head {
		t.Errorf("the merge's parents are %v, want %s and the approved commit %s", parents, remoteMain, head)
	}
	if message := repo.Git("--git-dir", bare, "log", "-1", "--format=%B", merged); !strings.Contains(message, "Ropeshift-Work-Order: "+started.WorkOrder) ||
		!strings.Contains(message, "Ropeshift-Reviewed-Commit: "+head) {
		t.Errorf("the merge commit reads %q", message)
	}
	if now := repo.Git("rev-parse", "main"); now != localMain {
		t.Errorf("the checkout's main moved from %s to %s", localMain, now)
	}
	view := in.acceptanceIn("shop", started.WorkOrder)
	if len(view.Integrations) != 1 || view.Integrations[0].ApprovedCommit != head || view.Integrations[0].MergedCommit != merged ||
		view.Integrations[0].ApprovedByKind != ledger.ApprovedByMember || view.Integrations[0].Plan == "" {
		t.Errorf("the approvals read %+v", view.Integrations)
	}
}
