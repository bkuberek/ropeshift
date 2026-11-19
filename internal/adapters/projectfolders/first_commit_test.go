package projectfolders_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// The first commit holds the files it was given and no other file of the folder: a file that was there before the
// write stays out of it, and so does one a person added after.
func TestFirstCommitAddsOnlyTheFilesItIsGiven(t *testing.T) {
	gittest.Require(t)
	env := gitEnv(t, identity, nil)
	f := projectfolders.Folders{Env: env}
	folder := filepath.Join(t.TempDir(), "harbor")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "notes.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}, {Path: "plan/goal.md", Text: "goal\n"}})
	spec := ports.CommitSpec{Paths: []string{"AGENTS.md", "plan/goal.md"}}
	if _, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, id, spec); err != nil {
		t.Fatal(err)
	}
	listed, err := (gitcli.Git{Dir: folder, Env: env}).Text(t.Context(), "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(listed), []string{"AGENTS.md", "plan/goal.md"}; !slices.Equal(got, want) {
		t.Errorf("the first commit holds %v, want %v", got, want)
	}
}

// A path is a file and never a pathspec: one that starts with a dash or names a glob adds what it names.
func TestFirstCommitReadsEachPathAsAFile(t *testing.T) {
	gittest.Require(t)
	env := gitEnv(t, identity, nil)
	f := projectfolders.Folders{Env: env}
	folder := filepath.Join(t.TempDir(), "harbor")
	id := put(t, f, folder, []ports.FolderFile{{Path: "-n.md", Text: "dash\n"}, {Path: "a*.md", Text: "glob\n"}, {Path: "ab.md", Text: "other\n"}})
	spec := ports.CommitSpec{Paths: []string{"-n.md", "a*.md"}}
	if _, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, id, spec); err != nil {
		t.Fatal(err)
	}
	listed, err := (gitcli.Git{Dir: folder, Env: env}).Text(t.Context(), "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(listed), []string{"-n.md", "a*.md"}; !slices.Equal(got, want) {
		t.Errorf("the first commit holds %v, want %v", got, want)
	}
}

// The repository starts on the branch git's configuration names as the default for a new repository, and on main when
// it names none.
func TestFirstCommitStartsOnTheConfiguredDefaultBranch(t *testing.T) {
	gittest.Require(t)
	for name, tc := range map[string]struct{ config, want string }{
		"configured": {identity + "[init]\n\tdefaultBranch = trunk\n", "trunk"},
		"none":       {identity, "main"},
	} {
		t.Run(name, func(t *testing.T) {
			env := gitEnv(t, tc.config, nil)
			f := projectfolders.Folders{Env: env}
			folder := filepath.Join(t.TempDir(), "harbor")
			id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}})
			if _, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, id, ports.CommitSpec{Paths: []string{"AGENTS.md"}}); err != nil {
				t.Fatal(err)
			}
			branch, err := (gitcli.Git{Dir: folder, Env: env}).Text(t.Context(), "symbolic-ref", "--short", "HEAD")
			if err != nil || branch != tc.want {
				t.Errorf("the repository is on %q (%v), want %q", branch, err, tc.want)
			}
		})
	}
}
