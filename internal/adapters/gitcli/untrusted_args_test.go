package gitcli_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/test/gittest"
)

// A fetch lists each ref on the remote with `--` before the remote and the ref, so no name is read as an option.
func TestFetchListsTheRemoteRefsAfterTheEndOfOptions(t *testing.T) {
	origin := gittest.New(t)
	origin.Branch("other", origin.Commit("README.txt", "one\n"))
	clone := origin.Clone("clone")
	ran := gittest.RecordGit(t)
	refs := []string{"refs/heads/other", "--upload-pack=true"}
	if err := (gitcli.Worktrees{Env: clone.Env()}).Fetch(t.Context(), clone.Dir, refs); err != nil {
		t.Fatal(err)
	}
	want := []string{"ls-remote -- origin refs/heads/other", "ls-remote -- origin --upload-pack=true"}
	if got := ran("ls-remote"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("git ran %q, want %q", got, want)
	}
}

// A remote that records no HEAD names the command that records it, with the remote's own name.
func TestDefaultTipNamesTheCommandThatRecordsTheRemoteHead(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	clone.Git("remote", "rename", "origin", "upstream")
	clone.Git("remote", "set-head", "upstream", "--delete")
	_, err := gitcli.Worktrees{Env: clone.Env()}.DefaultTip(t.Context(), clone.Dir)
	if err == nil || !strings.Contains(err.Error(), "git remote set-head upstream --auto") {
		t.Fatalf("a remote with no HEAD gave %v, want the command with the remote's name", err)
	}
	if !strings.Contains(err.Error(), "no HEAD") {
		t.Errorf("the error %q no longer says the remote records no HEAD", err)
	}
}
