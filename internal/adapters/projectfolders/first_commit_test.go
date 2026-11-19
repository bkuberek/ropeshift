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

// treeOf lists the files the first commit holds.
func treeOf(t *testing.T, env []string, folder string) []string {
	t.Helper()
	listed, err := (gitcli.Git{Dir: folder, Env: env}).Text(t.Context(), "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(listed)
}

// A file the person's global ignore file lists is skipped, as `git add -A` skipped it, and the commit goes on without
// it: a path git ignores is no reason for the commit to fail.
func TestFirstCommitSkipsAFileTheGlobalIgnoreFileLists(t *testing.T) {
	gittest.Require(t)
	excludes := filepath.Join(t.TempDir(), "ignore")
	if err := os.WriteFile(excludes, []byte("CLAUDE.md\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := gitEnv(t, identity+"[core]\n\texcludesFile = "+excludes+"\n", nil)
	f := projectfolders.Folders{Env: env}
	folder := filepath.Join(t.TempDir(), "harbor")
	id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}, {Path: "CLAUDE.md", Text: "@AGENTS.md\n"}})
	spec := ports.CommitSpec{Paths: []string{"AGENTS.md", "CLAUDE.md"}}
	if _, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, id, spec); err != nil {
		t.Fatalf("the first commit failed on an ignored file: %v", err)
	}
	if got, want := treeOf(t, env, folder), []string{"AGENTS.md"}; !slices.Equal(got, want) {
		t.Errorf("the first commit holds %v, want %v", got, want)
	}
}

// A .gitignore the folder holds already skips the files it lists in the same way.
func TestFirstCommitSkipsAFileTheFoldersGitignoreLists(t *testing.T) {
	gittest.Require(t)
	env := gitEnv(t, identity, nil)
	f := projectfolders.Folders{Env: env}
	folder := filepath.Join(t.TempDir(), "harbor")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, ".gitignore"), []byte("plan/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}, {Path: "plan/goal.md", Text: "goal\n"}})
	if _, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, id, ports.CommitSpec{Paths: []string{"AGENTS.md", "plan/goal.md"}}); err != nil {
		t.Fatalf("the first commit failed on an ignored file: %v", err)
	}
	// The folder's own .gitignore was there before the write and is not one of the paths, so it stays out of the commit.
	if got, want := treeOf(t, env, folder), []string{"AGENTS.md"}; !slices.Equal(got, want) {
		t.Errorf("the first commit holds %v, want %v", got, want)
	}
}

// With no file to add, whether every file was in the folder already or git ignores each, the commit says so and makes
// none, and a folder that was never a repository stays as it was.
func TestFirstCommitWithNoFileToAddSaysSoAndMakesNoCommit(t *testing.T) {
	gittest.Require(t)
	f := projectfolders.Folders{Env: gitEnv(t, identity, nil)}
	folder := filepath.Join(t.TempDir(), "harbor")
	id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}})
	_, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, id, ports.CommitSpec{})
	if err == nil || !strings.Contains(err.Error(), "no file to commit") {
		t.Errorf("a first commit of no file says %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(folder, ".git")); statErr == nil {
		t.Error("a first commit of no file made a repository")
	}
}

// The default branch is the one the global and system configuration name, and never one a clone around the folder
// names in its own configuration, which git init does not read.
func TestFirstCommitReadsTheDefaultBranchFromNoClone(t *testing.T) {
	gittest.Require(t)
	env := gitEnv(t, identity, nil)
	f := projectfolders.Folders{Env: env}
	clone := t.TempDir()
	around := gitcli.Git{Dir: clone, Env: env}
	for _, args := range [][]string{{"init", "--quiet"}, {"config", "init.defaultBranch", "elsewhere"}} {
		if _, err := around.Output(t.Context(), args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(clone, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(clone, "repos", "harbor")
	id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}})
	if _, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, id, ports.CommitSpec{Paths: []string{"AGENTS.md"}}); err != nil {
		t.Fatal(err)
	}
	branch, err := (gitcli.Git{Dir: folder, Env: env}).Text(t.Context(), "symbolic-ref", "--short", "HEAD")
	if err != nil || branch != "main" {
		t.Errorf("the repository is on %q (%v), want main", branch, err)
	}
}

// An empty value, and a value that is no branch name, leave the branch at main.
func TestFirstCommitFallsBackToMainForAnEmptyOrInvalidDefaultBranch(t *testing.T) {
	gittest.Require(t)
	for name, value := range map[string]string{"empty": "", "invalid": "bad name~", "a dash": "-x"} {
		t.Run(name, func(t *testing.T) {
			env := gitEnv(t, identity+"[init]\n\tdefaultBranch = "+value+"\n", nil)
			f := projectfolders.Folders{Env: env}
			folder := filepath.Join(t.TempDir(), "harbor")
			id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}})
			if _, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, id, ports.CommitSpec{Paths: []string{"AGENTS.md"}}); err != nil {
				t.Fatal(err)
			}
			branch, err := (gitcli.Git{Dir: folder, Env: env}).Text(t.Context(), "symbolic-ref", "--short", "HEAD")
			if err != nil || branch != "main" {
				t.Errorf("the repository is on %q (%v), want main", branch, err)
			}
		})
	}
}

// Held says which of the paths the folder holds, by looking at each one: a file, a folder, or a link is held, and a
// folder that cannot be read elsewhere in it changes nothing.
func TestHeldNamesTheShapePathsTheFolderHolds(t *testing.T) {
	gittest.Require(t)
	f := projectfolders.Folders{Env: gitEnv(t, identity, nil)}
	folder := filepath.Join(t.TempDir(), "harbor")
	for _, dir := range []string{"plan", "AGENTS.md.d", "locked"} {
		if err := os.MkdirAll(filepath.Join(folder, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(folder, "plan", "goal.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(folder, "CODEOWNERS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(folder, "locked"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(folder, "locked"), 0o755) })
	got, err := f.Held(t.Context(), folder, []string{"AGENTS.md", "CODEOWNERS", "plan/goal.md", "plan/plan.md"})
	if err != nil {
		t.Fatalf("a folder that cannot be read failed the read: %v", err)
	}
	if want := []string{"CODEOWNERS", "plan/goal.md"}; !slices.Equal(got, want) {
		t.Errorf("the folder holds %v, want %v", got, want)
	}
	if got, err := f.Held(t.Context(), filepath.Join(folder, "nothing"), []string{"AGENTS.md"}); err != nil || len(got) != 0 {
		t.Errorf("a folder that is not there holds %v (%v)", got, err)
	}
}

// A worker that died between git add and git commit finds the files in the index and no commit: the resume commits them,
// as a run that never died does, and an ignored file stays out.
func TestAResumedFirstCommitAfterTheAddEndsInACommit(t *testing.T) {
	gittest.Require(t)
	excludes := filepath.Join(t.TempDir(), "ignore")
	if err := os.WriteFile(excludes, []byte("CLAUDE.md\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := gitEnv(t, identity+"[core]\n\texcludesFile = "+excludes+"\n", nil)
	f := projectfolders.Folders{Env: env}
	folder := filepath.Join(t.TempDir(), "harbor")
	id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}, {Path: "CLAUDE.md", Text: "@AGENTS.md\n"}, {Path: "plan/goal.md", Text: "goal\n"}})
	git := gitcli.Git{Dir: folder, Env: env}
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "--", "AGENTS.md", "plan/goal.md"}} {
		if _, err := git.Output(t.Context(), args...); err != nil {
			t.Fatal(err)
		}
	}
	spec := ports.CommitSpec{Paths: []string{"AGENTS.md", "CLAUDE.md", "plan/goal.md"}}
	commit, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", true, id, spec)
	if err != nil || commit == "" {
		t.Fatalf("the resumed first commit is %q (%v)", commit, err)
	}
	if got, want := treeOf(t, env, folder), []string{"AGENTS.md", "plan/goal.md"}; !slices.Equal(got, want) {
		t.Errorf("the first commit holds %v, want %v", got, want)
	}
}

// An include in the global configuration is read, as git reads it: the branch an included file names is the one a new
// repository starts on.
func TestFirstCommitReadsTheDefaultBranchThroughAnInclude(t *testing.T) {
	gittest.Require(t)
	included := filepath.Join(t.TempDir(), "branch.gitconfig")
	if err := os.WriteFile(included, []byte("[init]\n\tdefaultBranch = trunk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := gitEnv(t, identity+"[include]\n\tpath = "+included+"\n", nil)
	f := projectfolders.Folders{Env: env}
	folder := filepath.Join(t.TempDir(), "harbor")
	id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}})
	if _, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, id, ports.CommitSpec{Paths: []string{"AGENTS.md"}}); err != nil {
		t.Fatal(err)
	}
	branch, err := (gitcli.Git{Dir: folder, Env: env}).Text(t.Context(), "symbolic-ref", "--short", "HEAD")
	if err != nil || branch != "trunk" {
		t.Errorf("the repository is on %q (%v), want trunk", branch, err)
	}
	if got := f.DefaultBranch(t.Context(), folder); got != "trunk" {
		t.Errorf("the default branch is %q, want trunk", got)
	}
}

// A first commit that names a branch starts the repository on it, whatever git's configuration names as the default, and
// one that names none keeps that default.
func TestFirstCommitStartsOnTheBranchItNames(t *testing.T) {
	gittest.Require(t)
	env := gitEnv(t, identity+"[init]\n\tdefaultBranch = devel\n", nil)
	f := projectfolders.Folders{Env: env}
	for name, tc := range map[string]struct{ branch, want string }{
		"named": {"trunk", "trunk"},
		"empty": {"", "devel"},
		"path":  {"release/v1", "release/v1"},
	} {
		t.Run(name, func(t *testing.T) {
			folder := filepath.Join(t.TempDir(), "harbor")
			id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}})
			spec := ports.CommitSpec{Paths: []string{"AGENTS.md"}, Branch: tc.branch}
			if _, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, id, spec); err != nil {
				t.Fatal(err)
			}
			branch, err := (gitcli.Git{Dir: folder, Env: env}).Text(t.Context(), "symbolic-ref", "--short", "HEAD")
			if err != nil || branch != tc.want {
				t.Errorf("HEAD names %q (%v), want %q", branch, err, tc.want)
			}
		})
	}
}

// A branch git refuses is an error before git init runs, so the folder holds no repository.
func TestFirstCommitRefusesABranchGitRefusesBeforeAnyInit(t *testing.T) {
	gittest.Require(t)
	env := gitEnv(t, identity, nil)
	f := projectfolders.Folders{Env: env}
	for name, branch := range map[string]string{
		"a space":       "bad name",
		"a tilde":       "bad~name",
		"a dash":        "-x",
		"a dot dot":     "a..b",
		"a lock":        "main.lock",
		"a reflog":      "@{-1}",
		"an at sign":    "@",
		"an empty part": "a//b",
	} {
		t.Run(name, func(t *testing.T) {
			folder := filepath.Join(t.TempDir(), "harbor")
			id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}})
			spec := ports.CommitSpec{Paths: []string{"AGENTS.md"}, Branch: branch}
			if _, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, id, spec); err == nil {
				t.Fatalf("a first commit on %q was made", branch)
			}
			if _, err := os.Lstat(filepath.Join(folder, ".git")); !os.IsNotExist(err) {
				t.Errorf("git init ran for %q: %v", branch, err)
			}
		})
	}
}
