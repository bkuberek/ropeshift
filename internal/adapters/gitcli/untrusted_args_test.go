package gitcli_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
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

// A commit lookup puts `--end-of-options` before the ref, so a name that reads as an option is never one.
func TestCommitLooksARefUpAfterTheEndOfOptions(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "one\n")
	ran := gittest.RecordGit(t)
	git := gitcli.Git{Dir: repo.Dir, Env: repo.Env()}
	if _, err := git.Commit(t.Context(), "--output=owned"); !errors.Is(err, gitcli.ErrNoRevision) {
		t.Fatalf("a ref named like an option gave %v, want ErrNoRevision", err)
	}
	want := []string{"rev-parse --verify --quiet --end-of-options --output=owned^{commit}"}
	if got := ran("rev-parse"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("git ran %q, want %q", got, want)
	}
}

// A fetch of a ref names the remote and the refspec after `--`, so a ref that reads as an option is never one.
func TestFetchNamesTheRemoteAndTheRefspecAfterTheEndOfOptions(t *testing.T) {
	origin := gittest.New(t)
	origin.Branch("other", origin.Commit("README.txt", "one\n"))
	clone := origin.Clone("clone")
	ran := gittest.RecordGit(t)
	if err := (gitcli.Worktrees{Env: clone.Env()}).Fetch(t.Context(), clone.Dir, []string{"refs/heads/other"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"fetch --quiet --no-tags -- origin refs/heads/other:refs/heads/other"}
	if got := ran("fetch"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("git ran %q, want %q", got, want)
	}
}

// A push names the remote and the refspec after `--`, so a ref that reads as an option is never one.
func TestPushNamesTheRemoteAndTheRefspecAfterTheEndOfOptions(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	ran := gittest.RecordGit(t)
	_ = gitcli.Worktrees{Env: clone.Env()}.Push(t.Context(), clone.Dir, "origin", "--delete")
	want := []string{"push --quiet --no-verify -- origin --delete:--delete"}
	if got := ran("push"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("git ran %q, want %q", got, want)
	}
}

// A snapshot records its ref with `--` before the name, so a name that reads as an option is never one.
func TestSnapshotNamesTheRefAfterTheEndOfOptions(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "one\n")
	ran := gittest.RecordGit(t)
	_, _ = gitcli.Worktrees{Env: repo.Env()}.Snapshot(t.Context(), repo.Dir, "--create-reflog")
	got := ran("update-ref")
	if len(got) != 1 || !strings.HasPrefix(got[0], "update-ref -- --create-reflog ") {
		t.Errorf("git ran %q, want one update-ref with -- before --create-reflog", got)
	}
}

// A worktree is added and removed with `--` before its path, so a path that reads as an option is never one.
func TestAWorktreeIsAddedAndRemovedAfterTheEndOfOptions(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "one\n")
	ran := gittest.RecordGit(t)
	path := filepath.Join(t.TempDir(), "lane")
	worktrees := gitcli.Worktrees{Env: repo.Env()}
	if _, err := worktrees.Create(t.Context(), ports.WorktreeSpec{Repository: repo.Dir, Path: path, Branch: "lane/one"}); err != nil {
		t.Fatal(err)
	}
	if err := worktrees.Remove(t.Context(), repo.Dir, path); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"add": "worktree add --quiet -b lane/one -- " + path + " HEAD", "remove": "worktree remove -- " + path}
	runs := ran("worktree")
	for verb, line := range want {
		found := false
		for _, run := range runs {
			found = found || run == line
		}
		if !found {
			t.Errorf("git ran %q, want a %s as %q", runs, verb, line)
		}
	}
}

// A remote whose name reads as an option is never handed to git, which would take it for one. The repository's
// remotes are read from its own configuration, so its name is data.
func TestARemoteNamedLikeAnOptionIsRefused(t *testing.T) {
	origin := gittest.New(t)
	origin.Branch("other", origin.Commit("README.txt", "one\n"))
	clone := origin.Clone("clone")
	clone.Git("remote", "remove", "origin")
	clone.Git("config", "remote.-upload-pack=true.url", origin.Dir)
	ran := gittest.RecordGit(t)
	err := (gitcli.Worktrees{Env: clone.Env()}).Fetch(t.Context(), clone.Dir, []string{"refs/heads/other"})
	if err == nil || !strings.Contains(err.Error(), "named like an option") {
		t.Fatalf("a remote named like an option gave %v, want a refusal that says so", err)
	}
	if got := append(ran("ls-remote"), ran("fetch")...); len(got) != 0 {
		t.Errorf("git ran %q with that remote, want nothing", got)
	}
}
