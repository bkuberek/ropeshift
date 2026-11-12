package gitcli_test

import (
	"os"
	"path/filepath"
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
	for _, name := range []string{"pre-commit", "commit-msg", "pre-merge-commit", "pre-push"} {
		hook := filepath.Join(r.repo.Dir, ".git", "hooks", name)
		if err := os.WriteFile(hook, []byte("#!/bin/sh\necho a repository hook ran >&2\nexit 1\n"), 0o700); err != nil {
			r.t.Fatal(err)
		}
	}
	defer func() {
		for _, name := range []string{"pre-commit", "commit-msg", "pre-merge-commit", "pre-push"} {
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

// TestAReviewOfAHeadOnlyTheRemoteHoldsFetchesIt makes a review's worktree detached at a commit someone pushed
// to the pull request's branch from another clone: the checkout fetches it from the remote first, because a
// moved head is reviewed again (ADR-0020).
func TestAReviewOfAHeadOnlyTheRemoteHoldsFetchesIt(t *testing.T) {
	r := newMergeRig(t, true)
	clone := filepath.Join(t.TempDir(), "clone")
	r.repo.Git("clone", "--quiet", "--branch", laneBranch, r.bare, clone)
	if err := os.WriteFile(filepath.Join(clone, "pushed.txt"), []byte("someone else's line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.repo.Git("-C", clone, "add", "pushed.txt")
	r.repo.Git("-C", clone, "commit", "--quiet", "-m", "someone else's push")
	r.repo.Git("-C", clone, "push", "--quiet", "origin", laneBranch)
	pushed := r.repo.Git("-C", clone, "rev-parse", "HEAD")
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
