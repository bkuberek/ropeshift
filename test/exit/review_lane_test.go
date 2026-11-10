package exit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// reviewsAndTries is a review lane that reaches for every write it was told it may not make, then writes a
// scratch file in its discarded worktree, then waits for the test to look at it before it reports.
const reviewsAndTries = `{
  "source": "a review lane that tries to write what it may not",
  "turns": [[
    {"init": true},
    {"tool": "Bash", "command": "git commit -q --allow-empty -m nope"},
    {"tool": "Write", "file": "scratch.txt"},
    {"tool": "Bash", "command": "git branch review-notes"},
    {"tool": "Bash", "command": "echo notes > notes.txt"},
    {"wait_file": "release-review"},
    {"mcp": "lane_report", "arguments": {"outcome": "completed", "verdicts": [{"criterion": "c1", "verdict": "pass", "reasons": "work.txt builds the retry", "citations": ["work.txt:1"]}]}},
    {"emit": {"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.02, "usage": {"input_tokens": 3, "output_tokens": 5}}}
  ]]
}`

// deniedCalls reads the reasons the fake CLI was given for the calls a hook denied.
func deniedCalls(in *install) []string {
	raw, err := os.ReadFile(filepath.Join(doubles.BinDir(in.path), "denied.log"))
	if err != nil {
		return nil
	}
	var out []string
	for line := range strings.Lines(string(raw)) {
		_, reason, _ := strings.Cut(strings.TrimSpace(line), " ")
		out = append(out, reason)
	}
	return out
}

// TestAReviewLaneReadsTheReviewedCommitAndLeavesNoBranch is FR-REVIEW-1 and FR-REVIEW-2 on the real binary
// with real git. A change lane commits a file and completes, and a review of that commit is made ready.
// Claimed and started, its lane stands in a worktree at that commit on no branch, with the diff in its
// context folder. It cannot commit, write with a tool, or make a branch, and when it ends its worktree is
// gone and the repository holds the same refs it held before, with the reviewed branch where it was.
func TestAReviewLaneReadsTheReviewedCommitAndLeavesNoBranch(t *testing.T) {
	in := newInstall(t)
	in.aLaneThatCommits()
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Commit("first.md", contract.ContractText("first", "First", "Build the first."))
	laneRoot := filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes")

	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	t.Cleanup(d.stop)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	ids := in.workOrders(1)
	order := ids["The work order of first"]
	in.must("work", "review", order)
	in.startLane(order, "c1", "lane-1")
	in.waitFor("the lane's outcome", func() bool { return in.show(order).State == workorders.Acceptance })

	// The lane's end made the review, ready, on the commit the lane left.
	ids = in.workOrders(2)
	reviewID := ids["Review: The work order of first"]
	review := in.show(reviewID)
	reviewed := branchesOf(in.acceptance(order))
	if len(reviewed) != 1 {
		t.Fatalf("the work left %+v", reviewed)
	}
	head, branch := reviewed[0].Digest, reviewed[0].URI
	if review.Kind != workorders.KindReview || review.State != workorders.Ready || review.Reviews != order || review.ContractVersion == 0 {
		t.Fatalf("the review reads %+v", review)
	}
	refs := func() string { return repo.Git("for-each-ref", "--format=%(refname) %(objectname)", "refs/") }
	before := refs()
	if got := repo.Git("rev-parse", "refs/heads/"+branch); got != head {
		t.Fatalf("the branch is at %s, the work says %s", got, head)
	}

	// The review is claimed and its lane started, as any other work's. Its lane tries every write.
	in.aReviewLane()
	in.must("work", "claim", reviewID, "--worker", in.workers()[0].ID, "--claim-id", "c2")
	in.must("lane", "start", reviewID, "--lane", "review-1")
	worktree := filepath.Join(laneRoot, "review-1")
	in.waitFor("the review lane's three denied calls", func() bool { return len(deniedCalls(in)) == 3 })
	in.waitFor("the diff in its context folder", func() bool {
		_, err := os.Stat(filepath.Join(worktree, "notes.txt"))
		return err == nil
	})

	view := in.lane("review-1")
	if view.BaseRef != branch || view.BaseCommit != head {
		t.Errorf("the review lane began at %s %s, want %s %s", view.BaseRef, view.BaseCommit, branch, head)
	}
	if got, ok := gitAt(repo, worktree, "rev-parse", "HEAD"); !ok || got != head {
		t.Errorf("the review lane stands at %q, want %s", got, head)
	}
	if name, ok := gitAt(repo, worktree, "branch", "--show-current"); !ok || name != "" {
		t.Errorf("the review lane's worktree is on branch %q", name)
	}
	diff, err := os.ReadFile(filepath.Join(worktree, lanes.ReviewDiffFile))
	if err != nil || !strings.Contains(string(diff), "+++ b/work.txt") || !strings.Contains(string(diff), "+work") {
		t.Errorf("the diff reads %q: %v", diff, err)
	}
	files, err := os.ReadFile(filepath.Join(worktree, lanes.ReviewFilesFile))
	if err != nil || string(files) != "work.txt\n" {
		t.Errorf("the changed files read %q: %v", files, err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "scratch.txt")); !os.IsNotExist(err) {
		t.Errorf("a tool wrote a file in a read-only lane: %v", err)
	}
	if got, ok := gitAt(repo, worktree, "log", "--format=%H", "-1"); !ok || got != head {
		t.Errorf("the review lane's worktree holds the commit %q after it tried to commit", got)
	}
	for _, reason := range deniedCalls(in) {
		if reason != lanes.ReasonReadOnly {
			t.Errorf("a call was denied for %q", reason)
		}
	}
	if got := refs(); got != before {
		t.Errorf("the refs were\n%s\nand are\n%s", before, got)
	}

	// It reports, and its work is done: the work it judged is still the person's to accept.
	if err := os.WriteFile(filepath.Join(doubles.BinDir(in.path), "release-review"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	in.waitFor("the review to be done", func() bool { return in.show(reviewID).State == workorders.Done })
	if state := in.show(order).State; state != workorders.Acceptance {
		t.Errorf("the reviewed work reads %s", state)
	}
	if branchesOf(in.acceptance(reviewID)) != nil {
		t.Errorf("the review's evidence reads %+v", in.acceptance(reviewID))
	}
	// What it said is a verdict on the work it judged, bound to the commit it read, and the work waits for
	// a member at the default merge authority.
	if judged := in.acceptance(order); len(judged.Verdicts) != 1 || judged.Verdicts[0].Verdict != workorders.VerdictPass || judged.Verdicts[0].Commit != head || judged.Verdicts[0].Review != reviewID {
		t.Errorf("the verdicts on the work read %+v, want one pass on %s", judged.Verdicts, head)
	}

	// The ledger asks the worker to take the worktree away, and it does: nothing of the review is left,
	// and the reviewed lane's own worktree is.
	in.waitFor("the review's worktree to go", func() bool {
		_, err := os.Stat(worktree)
		return os.IsNotExist(err)
	})
	if listed := repo.Git("worktree", "list", "--porcelain"); strings.Contains(listed, "review-1") || !strings.Contains(listed, "lane-1") {
		t.Errorf("the repository lists worktrees\n%s", listed)
	}
	if got := refs(); got != before {
		t.Errorf("after the review the refs were\n%s\nand are\n%s", before, got)
	}
	if got := repo.Git("rev-parse", "refs/heads/"+branch); got != head {
		t.Errorf("the reviewed branch moved from %s to %s", head, got)
	}
	if got := repo.Git("for-each-ref", "refs/ropeshift"); got != "" {
		t.Errorf("a review left refs of its own:\n%s", got)
	}
	if shown := in.lane("review-1"); !shown.Archived {
		t.Errorf("the ledger does not know the worktree went: %+v", shown)
	}
}

// aReviewLane replaces the lane the install's CLI double plays with one that reviews.
func (in *install) aReviewLane() {
	in.t.Helper()
	dir, _, _ := strings.Cut(in.path, string(os.PathListSeparator))
	if err := os.WriteFile(filepath.Join(dir, doubles.FixtureFile), []byte(reviewsAndTries), 0o600); err != nil {
		in.t.Fatal(err)
	}
}
