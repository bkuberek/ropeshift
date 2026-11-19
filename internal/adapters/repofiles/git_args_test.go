package repofiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/test/gittest"
)

// absent fails when the file exists, because git wrote it for an argument it read as `--output`.
func absent(t *testing.T, file string) {
	t.Helper()
	if _, err := os.Stat(file); err == nil {
		t.Errorf("git created %s, so it read a value that came from outside as an option", file)
	}
}

// Intake fetches the remote after `--`, so a remote whose name reads as an option is a name and never an
// option: here `--upload-pack`, which would run the script it names.
func TestIntakeFetchReadsARemoteNamedLikeAnOptionAsAName(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	ran := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "upload-pack")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n: > '"+ran+"'\nexec git-upload-pack \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	clone.Git("remote", "rename", "origin", "zorigin")
	clone.Git("config", "remote.--upload-pack="+script+".url", origin.Dir)
	clone.Git("config", "branch.main.remote", "zorigin")
	recorded := gittest.RecordGit(t)

	New(Config{Repository: clone.Dir, Env: clone.Env()}).fetch(t.Context())

	absent(t, ran)
	if got, want := recorded("fetch"), []string{"fetch --quiet -- --upload-pack=" + script}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("git ran %q, want %q", got, want)
	}
}

// A configured ref that reads as an option is a revision to git, which does not have it, and never an option: a
// ref of `--output=<file>` would create the file.
func TestARefThatReadsAsAnOptionIsNeverOneToRevList(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "one\n")
	root := repo.Roots("HEAD")[0]
	for name, identity := range map[string]string{"the first parent root": "", "a configured identity": root} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "owned")
			_, err := New(Config{Repository: repo.Dir, Env: repo.Env(), Ref: "--output=" + file, Identity: identity}).Instance(t.Context())
			if err == nil {
				t.Error("a ref that reads as an option was read as a revision, want an error")
			}
			absent(t, file)
		})
	}
}

// The history check puts `--end-of-options` before the commit, so a commit that reads as an option is a
// revision to git and never an option.
func TestHoldsReadsACommitThatReadsAsAnOptionAsARevision(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "one\n")
	blob := repo.Git("rev-parse", "HEAD:README.txt")
	file := filepath.Join(t.TempDir(), "owned")

	held, _ := New(Config{Repository: repo.Dir, Env: repo.Env()}).holds(t.Context(), "--output="+file, "README.txt", blob)

	if held {
		t.Error("a commit that reads as an option held the blob")
	}
	absent(t, file)
}
