package gitcli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// mergeRig is a checkout on main with a lane's branch that changes a file, and, when asked, a bare remote
// that holds both.
type mergeRig struct {
	t      *testing.T
	repo   *gittest.Repo
	merger gitcli.Worktrees
	bare   string
	head   string
}

const laneBranch = "lane/lane-1"

func newMergeRig(t *testing.T, remote bool) *mergeRig {
	t.Helper()
	repo := gittest.New(t)
	repo.Commit("README.md", "widgets\n")
	repo.Commit("work.txt", "first line\n")
	r := &mergeRig{t: t, repo: repo, merger: gitcli.Worktrees{Env: repo.Env()}}
	repo.Git("switch", "--quiet", "-c", laneBranch)
	r.head = repo.Commit("work.txt", "first line\nthe lane's line\n")
	repo.Git("switch", "--quiet", "main")
	if remote {
		r.bare = filepath.Join(t.TempDir(), "remote.git")
		repo.Git("init", "--quiet", "--bare", r.bare)
		repo.Git("remote", "add", "origin", r.bare)
		repo.Git("push", "--quiet", "origin", "main", laneBranch)
	}
	return r
}

func (r *mergeRig) merge(method ledger.MergeMethod) ledger.MergeResult {
	r.t.Helper()
	// Hooks in the checkout that would refuse every commit never run: a merge runs no repository code.
	for _, name := range []string{"pre-commit", "commit-msg", "pre-merge-commit", "pre-push", "reference-transaction"} {
		hook := filepath.Join(r.repo.Dir, ".git", "hooks", name)
		if err := os.WriteFile(hook, []byte("#!/bin/sh\necho a repository hook ran >&2\nexit 1\n"), 0o700); err != nil {
			r.t.Fatal(err)
		}
	}
	defer func() {
		for _, name := range []string{"pre-commit", "commit-msg", "pre-merge-commit", "pre-push", "reference-transaction"} {
			_ = os.Remove(filepath.Join(r.repo.Dir, ".git", "hooks", name))
		}
	}()
	result, err := r.merger.MergeBranch(r.t.Context(), r.repo.Dir, ports.BranchMerge{
		Branch: laneBranch, Base: "main", Commit: r.head, Method: method, WorkOrder: "wo-1", Title: "Add the lane's line",
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return result
}

// remoteMain is what the remote holds for main.
func (r *mergeRig) remoteMain() string {
	r.t.Helper()
	return r.repo.Git("--git-dir", r.bare, "rev-parse", "refs/heads/main")
}

// worktreesLeft lists the worktrees the checkout has, which a merge leaves as it found them.
func (r *mergeRig) worktreesLeft() int {
	r.t.Helper()
	return strings.Count(r.repo.Git("worktree", "list", "--porcelain"), "worktree ")
}

// TestABranchMergesIntoItsBaseOnTheRemote merges with a merge commit in a worktree of its own and pushes the
// result to the base branch, leaving the checkout and its branches where they were. The merge commit names
// the work order and the reviewed commit, so a repeat finds it and merges nothing twice.
func TestABranchMergesIntoItsBaseOnTheRemote(t *testing.T) {
	r := newMergeRig(t, true)
	before := r.repo.Git("rev-parse", "main")
	result := r.merge(ledger.MergeCommit)
	if result.Outcome != ledger.MergeMerged || result.MergeCommit == "" || result.MergedAt == "" {
		t.Fatalf("merge %+v", result)
	}
	if got := r.remoteMain(); got != result.MergeCommit {
		t.Fatalf("the remote's main is %s, want the merge %s", got, result.MergeCommit)
	}
	parents := strings.Fields(r.repo.Git("--git-dir", r.bare, "log", "-1", "--format=%P", result.MergeCommit))
	if len(parents) != 2 || parents[0] != before || parents[1] != r.head {
		t.Errorf("the merge's parents are %v, want %s and %s", parents, before, r.head)
	}
	message := r.repo.Git("--git-dir", r.bare, "log", "-1", "--format=%B", result.MergeCommit)
	for _, want := range []string{"Add the lane's line", "Ropeshift-Work-Order: wo-1", "Ropeshift-Reviewed-Commit: " + r.head} {
		if !strings.Contains(message, want) {
			t.Errorf("the merge's message leaves out %q:\n%s", want, message)
		}
	}
	if got := r.repo.Git("rev-parse", "main"); got != before {
		t.Errorf("the checkout's main moved to %s", got)
	}
	if r.worktreesLeft() != 1 {
		t.Errorf("the merge left worktrees: %s", r.repo.Git("worktree", "list"))
	}
	again := r.merge(ledger.MergeCommit)
	if again.Outcome != ledger.MergeMerged || again.MergeCommit != result.MergeCommit || r.remoteMain() != result.MergeCommit {
		t.Errorf("a repeat came to %+v, and the remote holds %s", again, r.remoteMain())
	}
}

// TestASquashAndARebaseLandOneLineOfHistory squash the branch into one commit on the base, and replay its
// commits onto a base that moved on, each carrying the trailers that make a repeat safe.
func TestASquashAndARebaseLandOneLineOfHistory(t *testing.T) {
	t.Run("squash", func(t *testing.T) {
		r := newMergeRig(t, true)
		before := r.remoteMain()
		result := r.merge(ledger.MergeSquash)
		parents := strings.Fields(r.repo.Git("--git-dir", r.bare, "log", "-1", "--format=%P", result.MergeCommit))
		if result.Outcome != ledger.MergeMerged || len(parents) != 1 || parents[0] != before {
			t.Fatalf("squash %+v with parents %v", result, parents)
		}
		if content := r.repo.Git("--git-dir", r.bare, "show", result.MergeCommit+":work.txt"); !strings.Contains(content, "the lane's line") {
			t.Errorf("the squash holds %q", content)
		}
		if again := r.merge(ledger.MergeSquash); again.MergeCommit != result.MergeCommit {
			t.Errorf("a repeat came to %+v", again)
		}
	})
	t.Run("rebase", func(t *testing.T) {
		r := newMergeRig(t, true)
		r.repo.Commit("other.txt", "someone else's work\n")
		r.repo.Git("push", "--quiet", "origin", "main")
		moved := r.remoteMain()
		result := r.merge(ledger.MergeRebase)
		parents := strings.Fields(r.repo.Git("--git-dir", r.bare, "log", "-1", "--format=%P", result.MergeCommit))
		if result.Outcome != ledger.MergeMerged || len(parents) != 1 || parents[0] != moved {
			t.Fatalf("rebase %+v with parents %v, want one on %s", result, parents, moved)
		}
		if message := r.repo.Git("--git-dir", r.bare, "log", "-1", "--format=%B", result.MergeCommit); !strings.Contains(message, "Ropeshift-Reviewed-Commit: "+r.head) {
			t.Errorf("the replayed commit reads %q", message)
		}
		if again := r.merge(ledger.MergeRebase); again.MergeCommit != result.MergeCommit {
			t.Errorf("a repeat came to %+v", again)
		}
	})
}

// landedSince lists the commits of the remote's base after a commit, oldest first.
func (r *mergeRig) landedSince(before, tip string) []string {
	r.t.Helper()
	return strings.Fields(r.repo.Git("--git-dir", r.bare, "rev-list", "--reverse", before+".."+tip))
}

// laneWithCommits adds commits to the lane's branch, and makes the last the reviewed commit.
func (r *mergeRig) laneWithCommits(messages ...string) {
	r.t.Helper()
	r.repo.Git("switch", "--quiet", laneBranch)
	for i, message := range messages {
		name := "step-" + strconv.Itoa(i) + ".txt"
		if err := os.WriteFile(filepath.Join(r.repo.Dir, name), []byte("step\n"), 0o600); err != nil {
			r.t.Fatal(err)
		}
		r.repo.Git("add", "--", name)
		r.repo.Git("commit", "--quiet", "-m", message)
	}
	r.head = r.repo.Git("rev-parse", "HEAD")
	r.repo.Git("switch", "--quiet", "main")
}

// TestARebaseCarriesTheTrailersOnEveryCommit replays three commits onto a base that moved on, and each of the three
// commits it lands names the work order and the reviewed commit, so none reads as a person's push. A repeat, with the
// base moved on again, finds the merge and lands nothing.
func TestARebaseCarriesTheTrailersOnEveryCommit(t *testing.T) {
	r := newMergeRig(t, true)
	r.laneWithCommits("add the first step", "add the second step\n\nIt follows the first.", "add the third step")
	r.repo.Commit("other.txt", "someone else's work\n")
	r.repo.Git("push", "--quiet", "origin", "main")
	moved := r.remoteMain()
	result := r.merge(ledger.MergeRebase)
	if result.Outcome != ledger.MergeMerged {
		t.Fatalf("rebase %+v", result)
	}
	landed := r.landedSince(moved, result.MergeCommit)
	if len(landed) != 4 {
		t.Fatalf("the rebase landed %d commits, want the lane's work.txt commit and its three", len(landed))
	}
	for _, commit := range landed {
		message := r.repo.Git("--git-dir", r.bare, "log", "-1", "--format=%B", commit)
		for _, want := range []string{"Ropeshift-Work-Order: wo-1", "Ropeshift-Reviewed-Commit: " + r.head} {
			if !strings.Contains(message, want) {
				t.Errorf("commit %s reads %q, which leaves out %q", commit[:8], message, want)
			}
		}
		if subject := r.repo.Git("--git-dir", r.bare, "log", "-1", "--format=%s", commit); strings.Contains(subject, "Ropeshift") {
			t.Errorf("a trailer is in the subject of %s: %q", commit[:8], subject)
		}
	}
	if body := r.repo.Git("--git-dir", r.bare, "log", "-1", "--format=%b", landed[2]); !strings.HasPrefix(body, "It follows the first.\n\nRopeshift-Work-Order: wo-1") {
		t.Errorf("the second step's body reads %q", body)
	}
	r.repo.Git("fetch", "--quiet", "origin")
	r.repo.Git("switch", "--quiet", "main")
	r.repo.Git("reset", "--quiet", "--hard", "origin/main")
	r.repo.Commit("later.txt", "someone else's later work\n")
	r.repo.Git("push", "--quiet", "origin", "main")
	tip := r.remoteMain()
	if again := r.merge(ledger.MergeRebase); again.Outcome != ledger.MergeMerged || again.MergeCommit != result.MergeCommit || r.remoteMain() != tip {
		t.Errorf("a repeat came to %+v, and the remote holds %s, not %s", again, r.remoteMain(), tip)
	}
}

// laneWithVerbatim adds a commit to the lane's branch for each message, stored as given: a commit's message is written
// with no clean-up, so the whitespace a person typed is what the lane holds. The last commit is the reviewed commit.
func (r *mergeRig) laneWithVerbatim(messages ...string) {
	r.t.Helper()
	r.repo.Git("switch", "--quiet", laneBranch)
	for i, message := range messages {
		name := "verbatim-" + strconv.Itoa(i) + ".txt"
		if err := os.WriteFile(filepath.Join(r.repo.Dir, name), []byte("step\n"), 0o600); err != nil {
			r.t.Fatal(err)
		}
		file := filepath.Join(r.t.TempDir(), "message")
		if err := os.WriteFile(file, []byte(message), 0o600); err != nil {
			r.t.Fatal(err)
		}
		r.repo.Git("add", "--", name)
		r.repo.Git("commit", "--quiet", "--cleanup=verbatim", "-F", file)
	}
	r.head = r.repo.Git("rev-parse", "HEAD")
	r.repo.Git("switch", "--quiet", "main")
}

// messageOf is the message of a commit in the remote, byte for byte: what follows the headers of the commit object.
func (r *mergeRig) messageOf(commit string) string {
	r.t.Helper()
	cmd := exec.Command("git", "--git-dir", r.bare, "cat-file", "commit", commit)
	cmd.Env = r.repo.Env()
	raw, err := cmd.Output()
	if err != nil {
		r.t.Fatal(err)
	}
	_, message, _ := strings.Cut(string(raw), "\n\n")
	return message
}

// A rebase keeps each commit's message as its author wrote it: hard-break spaces at the end of a line, blank lines in a
// row, tabs, and a line that starts with a hash stay as they were, whatever commit.cleanup says, and only the trailers
// are added after the message.
func TestARebaseKeepsEachMessageByteForByte(t *testing.T) {
	r := newMergeRig(t, true)
	first := "first step  \n\nA hard break  \nstays here,\ttabs too,\n\n\n# a line of the message, not a comment  \nand two blank lines stay.\n"
	second := "second step\n\nSigned-off-by: A Person <person@example.invalid>\nReviewed-by:   Another <other@example.invalid>  \n"
	r.laneWithVerbatim(first, second)
	r.repo.Git("config", "commit.cleanup", "strip")
	moved := r.remoteMain()
	result := r.merge(ledger.MergeRebase)
	landed := r.landedSince(moved, result.MergeCommit)
	if result.Outcome != ledger.MergeMerged || len(landed) != 3 {
		t.Fatalf("rebase %+v landed %v", result, landed)
	}
	trailers := ledger.MergeTrailers("wo-1", r.head)
	if got, want := r.messageOf(landed[1]), strings.TrimRight(first, "\n")+"\n\n"+trailers; got != want {
		t.Errorf("the first step reads\n%q\nwant\n%q", got, want)
	}
	if got, want := r.messageOf(landed[2]), second+trailers; got != want {
		t.Errorf("the second step reads\n%q\nwant\n%q", got, want)
	}
}

// A message too long for one argument of a command is replayed as it is: the message goes to git on its standard input.
func TestARebaseReplaysAMessageOf140Kilobytes(t *testing.T) {
	r := newMergeRig(t, true)
	big := "a long step\n\n" + strings.Repeat(strings.Repeat("x", 99)+"  \n", 1450)
	if len(big) < 140_000 {
		t.Fatalf("the message is %d bytes", len(big))
	}
	r.laneWithVerbatim(big)
	moved := r.remoteMain()
	result, err := r.merger.MergeBranch(t.Context(), r.repo.Dir, ports.BranchMerge{
		Branch: laneBranch, Base: "main", Commit: r.head, Method: ledger.MergeRebase, WorkOrder: "wo-1", Title: "Add the lane's line",
	})
	if err != nil || result.Outcome != ledger.MergeMerged {
		t.Fatalf("rebase %+v: %v", result, err)
	}
	landed := r.landedSince(moved, result.MergeCommit)
	if len(landed) != 2 {
		t.Fatalf("rebase landed %v", landed)
	}
	if got, want := r.messageOf(landed[1]), strings.TrimRight(big, "\n")+"\n\n"+ledger.MergeTrailers("wo-1", r.head); got != want {
		t.Errorf("the replayed message is %d bytes and differs from the %d it was", len(got), len(want))
	}
}

// A replayed commit that already ends in trailers keeps them, and the work order's trailers join the same block.
func TestARebaseJoinsTheTrailersAlreadyOnACommit(t *testing.T) {
	r := newMergeRig(t, true)
	r.laneWithCommits("add the step\n\nSigned-off-by: A Person <person@example.invalid>")
	moved := r.remoteMain()
	result := r.merge(ledger.MergeRebase)
	landed := r.landedSince(moved, result.MergeCommit)
	if result.Outcome != ledger.MergeMerged || len(landed) != 2 {
		t.Fatalf("rebase %+v landed %v", result, landed)
	}
	trailers := r.repo.Git("--git-dir", r.bare, "log", "-1", "--format=%(trailers:only,unfold)", landed[1])
	want := "Signed-off-by: A Person <person@example.invalid>\nRopeshift-Work-Order: wo-1\nRopeshift-Reviewed-Commit: " + r.head
	if trailers != want {
		t.Errorf("the trailers read\n%s\nwant\n%s", trailers, want)
	}
}

// TestAMergeThatCannotLandSaysWhy answers a conflict with the files in it, a branch that moved past the
// reviewed commit as a stale head, and a push the remote refuses as a refusal with what it said; each leaves
// the remote and the checkout's worktrees as they were.
func TestAMergeThatCannotLandSaysWhy(t *testing.T) {
	t.Run("a conflict", func(t *testing.T) {
		r := newMergeRig(t, true)
		r.repo.Commit("work.txt", "first line\nthe base's own line\n")
		r.repo.Git("push", "--quiet", "origin", "main")
		before := r.remoteMain()
		result := r.merge(ledger.MergeCommit)
		if result.Outcome != ledger.MergeConflict || len(result.Files) != 1 || result.Files[0] != "work.txt" {
			t.Fatalf("a conflicting merge came to %+v", result)
		}
		if r.remoteMain() != before || r.worktreesLeft() != 1 {
			t.Errorf("a conflict left the remote at %s and worktrees %s", r.remoteMain(), r.repo.Git("worktree", "list"))
		}
	})
	t.Run("a branch that moved", func(t *testing.T) {
		r := newMergeRig(t, true)
		r.repo.Git("switch", "--quiet", laneBranch)
		r.repo.Commit("more.txt", "unreviewed\n")
		r.repo.Git("switch", "--quiet", "main")
		if result := r.merge(ledger.MergeCommit); result.Outcome != ledger.MergeStaleHead || !strings.Contains(result.Detail, r.head[:8]) {
			t.Fatalf("a merge of a moved branch came to %+v", result)
		}
	})
	t.Run("a push the remote refuses", func(t *testing.T) {
		r := newMergeRig(t, true)
		hook := filepath.Join(r.bare, "hooks", "pre-receive")
		if err := os.WriteFile(hook, []byte("#!/bin/sh\necho main is protected >&2\nexit 1\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		before := r.remoteMain()
		result := r.merge(ledger.MergeCommit)
		if result.Outcome != ledger.MergeRefused || !strings.Contains(result.Detail, "main is protected") || r.remoteMain() != before {
			t.Fatalf("a refused push came to %+v, and the remote holds %s", result, r.remoteMain())
		}
	})
}

// TestABranchMergesIntoALocalBaseOnlyWhenNothingHasItCheckedOut moves the base branch of a repository with no
// remote by compare-and-swap, and holds the merge when the checkout has the base checked out, because moving
// it would change files under the person.
func TestABranchMergesIntoALocalBaseOnlyWhenNothingHasItCheckedOut(t *testing.T) {
	r := newMergeRig(t, false)
	if result := r.merge(ledger.MergeCommit); result.Outcome != ledger.MergeRefused || !strings.Contains(result.Detail, "checked out") {
		t.Fatalf("a merge into the checked-out base came to %+v", result)
	}
	before := r.repo.Git("rev-parse", "main")
	r.repo.Git("switch", "--quiet", "--detach")
	result := r.merge(ledger.MergeCommit)
	if result.Outcome != ledger.MergeMerged || r.repo.Git("rev-parse", "main") != result.MergeCommit {
		t.Fatalf("a merge into a local base came to %+v, and main is %s", result, r.repo.Git("rev-parse", "main"))
	}
	if parent := r.repo.Git("rev-parse", result.MergeCommit+"^1"); parent != before {
		t.Errorf("the merge sits on %s, want %s", parent, before)
	}
	if again := r.merge(ledger.MergeCommit); again.MergeCommit != result.MergeCommit {
		t.Errorf("a repeat came to %+v", again)
	}
}

// pushFromAnotherClone adds a commit to the lane's branch on the remote from a clone of its own, as someone
// does who pushes to a pull request's branch, and returns it. The checkout does not hold it.
func (r *mergeRig) pushFromAnotherClone() string {
	r.t.Helper()
	clone := filepath.Join(r.t.TempDir(), "clone")
	r.repo.Git("clone", "--quiet", "--branch", laneBranch, r.bare, clone)
	if err := os.WriteFile(filepath.Join(clone, "pushed.txt"), []byte("someone else's line\n"), 0o600); err != nil {
		r.t.Fatal(err)
	}
	r.repo.Git("-C", clone, "add", "pushed.txt")
	r.repo.Git("-C", clone, "commit", "--quiet", "-m", "someone else's push")
	r.repo.Git("-C", clone, "push", "--quiet", "origin", laneBranch)
	return r.repo.Git("-C", clone, "rev-parse", "HEAD")
}

// TestAReviewOfAHeadOnlyTheRemoteHoldsFetchesIt makes a review's worktree detached at a commit someone pushed
// to the pull request's branch from another clone: the checkout fetches it from the remote first, because a
// moved head is reviewed again (ADR-0020).
func TestAReviewOfAHeadOnlyTheRemoteHoldsFetchesIt(t *testing.T) {
	r := newMergeRig(t, true)
	pushed := r.pushFromAnotherClone()
	path := filepath.Join(t.TempDir(), "review")
	tree, err := r.merger.Create(t.Context(), ports.WorktreeSpec{Repository: r.repo.Dir, Path: path, Base: pushed, Detached: true})
	if err != nil {
		t.Fatal(err)
	}
	if tree.Head != pushed {
		t.Errorf("the review's worktree is at %s, want %s", tree.Head, pushed)
	}
	if got := r.repo.Git("rev-parse", "refs/heads/"+laneBranch); got != r.head {
		t.Errorf("fetching moved the checkout's branch to %s", got)
	}
}

// The fetch of a review's head brings every branch of the remote into its remote-tracking ref, as intake's
// fetch of the clone does, so the review's fetch can lose git's lock on the lane's ref to it. That is not a
// head the checkout cannot read.
func TestAReviewFetchesItsHeadThroughAFetchThatHoldsTheRefsLock(t *testing.T) {
	r := newMergeRig(t, true)
	pushed := r.pushFromAnotherClone()
	r.repo.HoldRefLock("refs/remotes/origin/"+laneBranch, lockHeld)
	merger := gitcli.Worktrees{Env: r.repo.EnvWithoutRefLockWait()}
	tree, err := merger.Create(t.Context(), ports.WorktreeSpec{Repository: r.repo.Dir, Path: filepath.Join(t.TempDir(), "review"), Base: pushed, Detached: true})
	if err != nil {
		t.Fatal(err)
	}
	if tree.Head != pushed {
		t.Errorf("the review's worktree is at %s, want %s", tree.Head, pushed)
	}
}

// A merge reads the base's tip with a fetch of the branch, which also moves the base's remote-tracking ref,
// so it can lose git's lock on the ref to another fetch. It must merge onto the moved base, not fail.
func TestAMergeReadsItsBaseThroughAFetchThatHoldsTheRefsLock(t *testing.T) {
	r := newMergeRig(t, true)
	moved := (&gittest.Repo{T: t, Dir: r.bare, Home: r.repo.Home}).Advance("main")
	r.repo.HoldRefLock("refs/remotes/origin/main", lockHeld)
	r.merger = gitcli.Worktrees{Env: r.repo.EnvWithoutRefLockWait()}
	result := r.merge(ledger.MergeCommit)
	if result.Outcome != ledger.MergeMerged || result.MergeCommit == "" {
		t.Fatalf("merge %+v", result)
	}
	if got := r.remoteMain(); got != result.MergeCommit {
		t.Fatalf("the remote's main is %s, want the merge %s", got, result.MergeCommit)
	}
	if parent := r.repo.Git("--git-dir", r.bare, "rev-parse", result.MergeCommit+"^1"); parent != moved {
		t.Errorf("the merge sits on %s, want the moved base %s", parent, moved)
	}
}

// A merge reads the remote's tip of the base with `--` before the remote, so no name is read as an option.
func TestAMergeListsTheRemotesBaseAfterTheEndOfOptions(t *testing.T) {
	r := newMergeRig(t, true)
	ran := gittest.RecordGit(t)
	r.merge(ledger.MergeCommit)
	want := []string{"ls-remote -- origin refs/heads/main"}
	if got := ran("ls-remote"); !slices.Equal(got, want) {
		t.Errorf("git ran %q, want %q", got, want)
	}
}

// A reviewed commit that is no commit ID is never handed to git, where it could read as an option. It names no
// commit the branch is at, so the merge is stale.
func TestMergeBranchNeverPassesAReviewedCommitThatIsNoIDToGit(t *testing.T) {
	r := newMergeRig(t, false)
	runs := gittest.RecordGit(t)
	r.merger = gitcli.Worktrees{Env: r.repo.Env()}
	for _, commit := range []string{"--all", "--is-ancestor", "-h"} {
		result, err := r.merger.MergeBranch(t.Context(), r.repo.Dir, ports.BranchMerge{
			Branch: laneBranch, Base: "main", Commit: commit, Method: ledger.MergeCommit, WorkOrder: "wo-1", Title: "Add the lane's line",
		})
		if err != nil || result.Outcome != ledger.MergeStaleHead {
			t.Errorf("a reviewed commit %q gives %+v, %v; want a stale head", commit, result, err)
		}
		for _, run := range runs("merge-base") {
			if strings.Contains(run, " "+commit+" ") || strings.HasSuffix(run, " "+commit) {
				t.Errorf("git ran %q", run)
			}
		}
	}
}
