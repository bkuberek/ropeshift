package gitcli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/test/gittest"
)

// recordGit puts a `git` on PATH that appends each run's arguments to a log, one run a line, and runs the real one.
func recordGit(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func recorded(t *testing.T, log, verb string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var runs []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, verb+" ") {
			runs = append(runs, line)
		}
	}
	return runs
}

// A fetch lists each ref on the remote with `--` before the remote and the ref, so no name is read as an option.
func TestFetchListsTheRemoteRefsAfterTheEndOfOptions(t *testing.T) {
	origin := gittest.New(t)
	origin.Branch("other", origin.Commit("README.txt", "one\n"))
	clone := origin.Clone("clone")
	log := recordGit(t)
	refs := []string{"refs/heads/other", "--upload-pack=true"}
	if err := (gitcli.Worktrees{Env: clone.Env()}).Fetch(t.Context(), clone.Dir, refs); err != nil {
		t.Fatal(err)
	}
	want := []string{"ls-remote -- origin refs/heads/other", "ls-remote -- origin --upload-pack=true"}
	if got := recorded(t, log, "ls-remote"); strings.Join(got, "\n") != strings.Join(want, "\n") {
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
