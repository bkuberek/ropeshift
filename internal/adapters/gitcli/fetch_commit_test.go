package gitcli_test

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// A review reads a head another clone pushed, so FetchCommit brings the commit from the remote, and a commit the
// clone already holds, or a clone with no remote, needs nothing.
func TestFetchCommitBringsAHeadAnotherCloneKept(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	pushed := origin.Commit("README.txt", "two\n")
	worktrees := gitcli.Worktrees{Env: clone.Env()}

	if _, err := (gitcli.Git{Dir: clone.Dir, Env: clone.Env()}).Commit(t.Context(), pushed); err == nil {
		t.Fatal("the clone already holds the commit")
	}
	if err := worktrees.FetchCommit(t.Context(), clone.Dir, pushed); err != nil {
		t.Fatal(err)
	}
	if _, err := (gitcli.Git{Dir: clone.Dir, Env: clone.Env()}).Commit(t.Context(), pushed); err != nil {
		t.Errorf("the clone lacks the commit after the fetch: %v", err)
	}
	if err := worktrees.FetchCommit(t.Context(), clone.Dir, pushed); err != nil {
		t.Errorf("a commit the clone holds gave %v", err)
	}
}

func TestFetchCommitNeedsNothingWithoutARemoteAndRefusesAnOption(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "one\n")
	worktrees := gitcli.Worktrees{Env: repo.Env()}
	if err := worktrees.FetchCommit(t.Context(), repo.Dir, "0123456789abcdef0123456789abcdef01234567"); err != nil {
		t.Errorf("a repository with no remote gave %v", err)
	}
	if err := worktrees.FetchCommit(t.Context(), repo.Dir, "--upload-pack=x"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("an option gave %v", err)
	}
}
