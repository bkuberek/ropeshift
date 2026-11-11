package exit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// commitsAFile is a lane that writes a file and commits it on its own branch, then reports. A lane that
// starts where another left the file still commits, because the commit may be empty.
const commitsAFile = `{
  "source": "a lane that leaves a commit on its branch",
  "turns": [[
    {"init": true},
    {"tool": "Bash", "command": "echo work > work.txt && git add work.txt && git commit -q --allow-empty -m work"},
    {"mcp": "lane_report", "arguments": {"outcome": "completed", "artifacts": ["branch"], "checks": [{"name": "stand-in", "passed": true}]}},
    {"emit": {"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.02, "usage": {"input_tokens": 3, "output_tokens": 5}}}
  ]]
}`

// aLaneThatCommits installs a double whose lanes each commit one file, and gives git an identity in this
// install's home, because a lane runs with it.
func (in *install) aLaneThatCommits() {
	in.t.Helper()
	in.path = doubles.InstallFakeClaude(in.t, "lane-completed")
	dir, _, _ := strings.Cut(in.path, string(os.PathListSeparator))
	if err := os.WriteFile(filepath.Join(dir, doubles.FixtureFile), []byte(commitsAFile), 0o600); err != nil {
		in.t.Fatal(err)
	}
	config := "[user]\n\tname = Lane\n\temail = lane@example.invalid\n"
	if err := os.WriteFile(filepath.Join(in.home, ".gitconfig"), []byte(config), 0o600); err != nil {
		in.t.Fatal(err)
	}
}

// startLane claims a work order for the install's worker and starts a lane. A lane's shim outlives
// the daemon by design, so a test that fails before its lane ends must still end the shim.
func (in *install) startLane(id, claim, lane string) {
	in.t.Helper()
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", claim)
	in.must("lane", "start", id, "--lane", lane)
	doubles.ReapShims(in.t, filepath.Join(in.paths().StateDir, "lanes", lane))
}

// dependentContract is a contract whose one work order waits for another's.
func dependentContract(id, title, dependsOn string) string {
	text := contract.ContractText(id, title, "Build on the first.")
	return strings.Replace(text, "size = \"small\"", "size = \"small\"\ndepends_on = [\""+dependsOn+"\"]", 1)
}

// TestADependentLaneStartsFromItsDependencysWork is FR-BASE-1 and FR-BASE-3 on the real binary, with real
// git. The checkout sits on a branch of its own with a commit nobody asked a lane to build on. The first
// lane starts from the base branch and commits a file. Work that depends on it is refused until a person
// merges that branch, with the reason on the work order, and then its lane starts from the base that
// holds the file.
func TestADependentLaneStartsFromItsDependencysWork(t *testing.T) {
	in := newInstall(t)
	in.aLaneThatCommits()
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Commit("first.md", contract.ContractText("first", "First", "Build the first."))
	repo.Commit("second.md", dependentContract("second", "Second", "first-order"))
	first := repo.Git("rev-parse", "main")
	repo.Git("switch", "--quiet", "-c", "scratch")
	repo.Commit("scratch.txt", "not for lanes\n")
	laneRoot := filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes")

	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	t.Cleanup(d.stop)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	ids := in.workOrders(2)
	firstOrder, secondOrder := ids["The work order of first"], ids["The work order of second"]
	in.must("work", "review", firstOrder)
	in.startLane(firstOrder, "c1", "lane-1")
	in.waitFor("the first lane's outcome", func() bool { return in.show(firstOrder).State == workorders.Acceptance })

	// The first lane began on the base branch, and not on the checkout's scratch branch.
	one := in.lane("lane-1")
	if one.BaseRef != "main" || one.BaseCommit != first {
		t.Errorf("the first lane began at %s %s, want main %s", one.BaseRef, one.BaseCommit, first)
	}
	if _, err := os.Stat(filepath.Join(laneRoot, "lane-1", "scratch.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the checkout's own branch reached a lane: %v", err)
	}
	if text := in.must("lane", "show", "lane-1"); !strings.Contains(text, "base\tmain\t"+first) {
		t.Errorf("lane show does not say where the lane began:\n%s", text)
	}
	in.must("work", "accept", firstOrder)
	accepted := branchesOf(in.acceptance(firstOrder))[0].Digest
	if accepted == first {
		t.Fatal("the first lane left no commit, so nothing below shows a wait")
	}

	// The accepted work is not in the base yet, so work that builds on it waits and says why.
	in.must("work", "review", secondOrder)
	code, _, stderr := in.run("work", "claim", secondOrder, "--worker", in.workers()[0].ID, "--claim-id", "c2")
	if code != cli.ExitConflict || !strings.Contains(stderr, string(workorders.HoldWaitingForMerge)) || !strings.Contains(stderr, firstOrder) {
		t.Fatalf("a claim on work that waits: exit %d: %s", code, stderr)
	}
	held := in.show(secondOrder)
	if held.State != workorders.Ready || held.StartHeld == nil || held.StartHeld.Reason != workorders.HoldWaitingForMerge || held.StartHeld.Dependency != firstOrder {
		t.Fatalf("the work order reads %s with %+v", held.State, held.StartHeld)
	}
	if text := in.must("work", "show", secondOrder); !strings.Contains(text, "waiting\twaiting_for_merge\t"+firstOrder) {
		t.Errorf("work show does not say why:\n%s", text)
	}

	// A person merges the dependency's branch, and the reason clears on its own.
	repo.Git("switch", "--quiet", "main")
	merged := func() string {
		repo.Git("merge", "--quiet", "--no-ff", "-m", "merge the first", "lane/lane-1")
		return repo.Git("rev-parse", "main")
	}()
	in.waitFor("the reason to clear", func() bool { return in.show(secondOrder).StartHeld == nil })
	in.startLane(secondOrder, "c3", "lane-2")
	in.waitFor("the second lane's outcome", func() bool { return in.show(secondOrder).State == workorders.Acceptance })
	two := in.lane("lane-2")
	if two.BaseRef != "main" || two.BaseCommit != merged {
		t.Errorf("the second lane began at %s %s, want main %s", two.BaseRef, two.BaseCommit, merged)
	}
	if contents, err := os.ReadFile(filepath.Join(laneRoot, "lane-2", "work.txt")); err != nil || strings.TrimSpace(string(contents)) != "work" {
		t.Errorf("the second lane does not hold the first lane's file: %q, %v", contents, err)
	}
	if got := repo.Git("merge-base", "--is-ancestor", accepted, two.BaseCommit); got != "" {
		t.Errorf("the second lane's base does not hold the accepted commit: %s", got)
	}
}

// TestALaneStartsFromWhatTheRemoteHolds is FR-BASE-1 against a checkout that is ahead of its remote, as a
// person's working branch often is: the lane starts at the commit the remote holds, and not at the
// checkout's own.
func TestALaneStartsFromWhatTheRemoteHolds(t *testing.T) {
	in := newInstall(t)
	repo, _, pushed := repoAheadOfItsRemote(t)
	worktree := runLaneThatLeavesWork(t, in, repo)
	view := in.lane("lane-1")
	if view.BaseRef != "origin/main" || view.BaseCommit != pushed {
		t.Errorf("the lane began at %s %s, want origin/main %s", view.BaseRef, view.BaseCommit, pushed)
	}
	if got := repo.Git("rev-parse", "refs/heads/lane/lane-1"); got != pushed {
		t.Errorf("the lane's branch is at %s, want the remote's %s", got, pushed)
	}
	if _, err := os.Stat(filepath.Join(worktree, "notes.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a commit the remote lacks reached the lane: %v", err)
	}
}

// TestARepositoryRecordIsReadAndChangedByAMember is FR-REPO-1 on the real binary: the daemon reads the
// default branch from git, a member changes the rest, and a lane's own credential changes nothing.
func TestARepositoryRecordIsReadAndChangedByAMember(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	d := in.start("--intake-tick", "20ms", "--source", repo.Dir)
	t.Cleanup(d.stop)
	in.waitReady(d)
	var list api.RepositoryList
	in.waitFor("the repository record", func() bool {
		if err := json.Unmarshal([]byte(in.must("repository", "list", "--json")), &list); err != nil {
			t.Fatal(err)
		}
		return len(list.Repositories) == 1
	})
	record := list.Repositories[0]
	if record.DefaultBranch != "main" || record.Base != "main" || record.MergeMethod != ledger.MergeCommit || record.StartFrom != ledger.StartFromMerged || record.Target() != ledger.ReviewOnBranch {
		t.Fatalf("the record reads %+v", record)
	}
	in.must("repository", "set", record.ID[:8], "--base", "release/1.2", "--review-target", "pull_request")
	var shown api.RepositoryView
	if err := json.Unmarshal([]byte(in.must("repository", "show", record.ID, "--json")), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Base != "release/1.2" || shown.Target() != ledger.ReviewOnPullRequest || shown.DefaultBranch != "main" {
		t.Errorf("after the change the record reads %+v", shown)
	}
	if code, _, stderr := in.run("repository", "set", record.ID, "--merge-method", "squash", "--start-from", "accepted"); code != cli.ExitUsage || !strings.Contains(stderr, "rewrites") {
		t.Errorf("accepted work on a squash repository: exit %d: %s", code, stderr)
	}
	// Intake reads the source again, and a member's settings stay.
	repo.Commit("another.md", contract.ContractText("beta", "Beta", "Another."))
	in.workOrders(2)
	if err := json.Unmarshal([]byte(in.must("repository", "show", record.ID, "--json")), &shown); err != nil || shown.Base != "release/1.2" {
		t.Errorf("a later read changed what a member set: %+v: %v", shown, err)
	}
	code, _, _ := in.runWith(nil, "", []string{authority.EnvMemberToken + "=a-lane-token"}, "repository", "set", record.ID, "--review-target", "branch")
	if code != cli.ExitForbidden {
		t.Errorf("a lane's token changing a repository: exit %d", code)
	}
}
