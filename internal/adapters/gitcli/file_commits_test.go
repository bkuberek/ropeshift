package gitcli_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

const workspaceSubject = "chore: add the workspace files"

func (r *guardRig) read(name string) (string, bool) {
	r.t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.path, name))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return string(raw), true
}

// The files go in one commit with the subject the worker names, made in the product's name and not the person's,
// and the worktree is clean afterward.
func TestCommitFilesAddsEachFileInOneCommitInTheProductsName(t *testing.T) {
	r := newGuardRig(t)
	files := []ports.TreeFile{
		{Path: "plan/repositories.md", Text: "+++\nkind = \"repositories\"\n+++\n"},
		{Path: ".gitignore", Lines: []string{"repos/", "/.ropeshift/worktrees/"}},
		{Path: "Makefile", Text: "setup:\n\t@true\n"},
	}
	made, err := r.worktrees.CommitFiles(t.Context(), r.path, files, workspaceSubject)
	if err != nil {
		t.Fatal(err)
	}
	head := r.inWorktree("rev-parse", "HEAD")
	if made.Hash != head || made.Subject != workspaceSubject {
		t.Errorf("the commit reads %+v, and HEAD is %s", made, head)
	}
	if want := []string{".gitignore", "Makefile", "plan/repositories.md"}; strings.Join(made.Files, ",") != strings.Join(want, ",") {
		t.Errorf("the commit holds %v, want %v", made.Files, want)
	}
	if count := r.inWorktree("rev-list", "--count", "HEAD"); count != "2" {
		t.Errorf("the branch holds %s commits, want the seed and one", count)
	}
	if author := r.inWorktree("log", "-1", "--format=%an <%ae>|%cn <%ce>"); author != "ropeshift <ropeshift@localhost>|ropeshift <ropeshift@localhost>" {
		t.Errorf("the commit is by %q", author)
	}
	if text, _ := r.read("Makefile"); text != "setup:\n\t@true\n" {
		t.Errorf("the Makefile reads %q", text)
	}
	if text, _ := r.read(".gitignore"); text != "repos/\n/.ropeshift/worktrees/\n" {
		t.Errorf("the ignore file reads %q", text)
	}
	if status := r.inWorktree("status", "--porcelain"); status != "" {
		t.Errorf("the worktree is dirty after the commit:\n%s", status)
	}
}

// A file the worktree holds is left as it is, and a line it holds is not added again, each only when absent.
func TestCommitFilesLeavesWhatTheWorktreeHoldsAlone(t *testing.T) {
	r := newGuardRig(t)
	r.repo.Commit("Makefile", "all:\n")
	r.repo.Commit(".gitignore", "node_modules/\nrepos/")
	r.inWorktree("merge", "--quiet", "--ff-only", "main")
	files := []ports.TreeFile{
		{Path: "Makefile", Text: "setup:\n"},
		{Path: ".gitignore", Lines: []string{"repos/", "/.ropeshift/worktrees/"}},
	}
	made, err := r.worktrees.CommitFiles(t.Context(), r.path, files, workspaceSubject)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".gitignore"}; strings.Join(made.Files, ",") != strings.Join(want, ",") {
		t.Errorf("the commit holds %v, want only %v", made.Files, want)
	}
	if text, _ := r.read("Makefile"); text != "all:\n" {
		t.Errorf("an existing Makefile reads %q", text)
	}
	if text, _ := r.read(".gitignore"); text != "node_modules/\nrepos/\n/.ropeshift/worktrees/\n" {
		t.Errorf("the ignore file reads %q: the line it held stays once, and a file with no final newline still gains its line on a line of its own", text)
	}
}

// A retry after a crash that left the commit makes no second one: every file is held, so nothing changes, and
// what comes back is empty. The same goes for a crash that wrote the files and never committed them, which the
// retry commits once.
func TestCommitFilesRepeatedMakesNoSecondCommit(t *testing.T) {
	r := newGuardRig(t)
	files := []ports.TreeFile{{Path: "Makefile", Text: "setup:\n"}, {Path: ".gitignore", Lines: []string{"repos/"}}}
	first, err := r.worktrees.CommitFiles(t.Context(), r.path, files, workspaceSubject)
	if err != nil || first.Hash == "" {
		t.Fatalf("the first commit: %+v, %v", first, err)
	}
	again, err := r.worktrees.CommitFiles(t.Context(), r.path, files, workspaceSubject)
	if err != nil || again.Hash != "" || len(again.Files) != 0 {
		t.Errorf("the repeat made %+v: %v", again, err)
	}
	if count := r.inWorktree("rev-list", "--count", "HEAD"); count != "2" {
		t.Errorf("the branch holds %s commits after a repeat, want 2", count)
	}

	// A crash between the writes and the commit leaves the files in the worktree, untracked and as the worker
	// would have written them.
	other := newGuardRig(t)
	other.write("Makefile", "setup:\n")
	other.write(".gitignore", "repos/\n")
	made, err := other.worktrees.CommitFiles(t.Context(), other.path, files, workspaceSubject)
	if err != nil || made.Hash == "" || len(made.Files) != 2 {
		t.Errorf("the commit after a crash that wrote the files: %+v, %v", made, err)
	}
	// A file that is there and is not the worker's is not written over, and not committed.
	third := newGuardRig(t)
	third.write("Makefile", "mine:\n")
	made, err = third.worktrees.CommitFiles(t.Context(), third.path, []ports.TreeFile{{Path: "Makefile", Text: "setup:\n"}}, workspaceSubject)
	if err != nil || made.Hash != "" {
		t.Errorf("a file that is not the worker's was committed: %+v, %v", made, err)
	}
	if text, _ := third.read("Makefile"); text != "mine:\n" {
		t.Errorf("the file reads %q", text)
	}
}

// A repository's hooks and its settings for signing run nothing and ask for nothing, because the lane's code
// must not run before the lane does.
func TestCommitFilesRunsNoHookAndAsksForNoSignature(t *testing.T) {
	r := newGuardRig(t)
	hooks := filepath.Join(r.repo.Dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(r.repo.Home, "hook-ran")
	script := "#!/bin/sh\necho ran > " + marker + "\nexit 1\n"
	for _, hook := range []string{"pre-commit", "commit-msg", "post-commit"} {
		if err := os.WriteFile(filepath.Join(hooks, hook), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	r.repo.Git("config", "commit.gpgsign", "true")
	r.repo.Git("config", "gpg.program", "/nonexistent/gpg")
	if _, err := r.worktrees.CommitFiles(t.Context(), r.path, []ports.TreeFile{{Path: "Makefile", Text: "setup:\n"}}, workspaceSubject); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a hook of the repository ran")
	}
}

// Commits lists what the branch holds beyond a base, newest first, with the files each changed, and a worktree
// that is gone is not found.
func TestCommitsListsTheBranchBeyondItsBase(t *testing.T) {
	r := newGuardRig(t)
	base := r.inWorktree("rev-parse", "HEAD")
	if _, err := r.worktrees.CommitFiles(t.Context(), r.path, []ports.TreeFile{{Path: "Makefile", Text: "setup:\n"}}, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.worktrees.CommitFiles(t.Context(), r.path, []ports.TreeFile{{Path: "notes.md", Text: "n\n"}, {Path: ".gitignore", Lines: []string{"x/"}}}, workspaceSubject); err != nil {
		t.Fatal(err)
	}
	got, err := r.worktrees.Commits(t.Context(), r.path, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Subject != workspaceSubject || got[1].Subject != "first" {
		t.Fatalf("the branch holds %+v beyond its base", got)
	}
	if strings.Join(got[0].Files, ",") != ".gitignore,notes.md" || strings.Join(got[1].Files, ",") != "Makefile" || got[0].Hash != r.inWorktree("rev-parse", "HEAD") {
		t.Errorf("the commits read %+v", got)
	}
	head := r.inWorktree("rev-parse", "HEAD")
	if none, err := r.worktrees.Commits(t.Context(), r.path, head); err != nil || len(none) != 0 {
		t.Errorf("beyond its own tip the branch holds %+v: %v", none, err)
	}
	if every, err := r.worktrees.Commits(t.Context(), r.path, ""); err != nil || len(every) != 3 {
		t.Errorf("with no base the branch lists %+v: %v", every, err)
	}
	if _, err := r.worktrees.Commits(t.Context(), filepath.Join(r.path, "gone"), base); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a worktree that is gone: %v", err)
	}
	if _, err := r.worktrees.Commits(t.Context(), r.path, "--output=x"); err == nil {
		t.Error("a base that is an option was accepted")
	}
}

// TestCommitsNameAFileWithALineBreakAsOneFile reads names NUL-separated, so a name with a line break in it is one name
// and never two.
func TestCommitsNameAFileWithALineBreakAsOneFile(t *testing.T) {
	r := newGuardRig(t)
	base := r.inWorktree("rev-parse", "HEAD")
	r.commit("x\ny", "a\n")
	got, err := r.worktrees.Commits(t.Context(), r.path, base)
	if err != nil || len(got) != 1 || len(got[0].Files) != 1 || got[0].Files[0] != "x\ny" {
		t.Errorf("the branch holds %+v beyond its base: %v", got, err)
	}
}

// TestBranchChangesReadsABranchInItsRepository lists a lane's branch beyond its base from the repository it is
// pushed from, whatever that checkout has checked out, and a branch it does not hold is ErrNotFound.
func TestBranchChangesReadsABranchInItsRepository(t *testing.T) {
	r := newGuardRig(t)
	base := r.inWorktree("rev-parse", "HEAD")
	r.commit("docs/a.md", "a\n")
	got, err := r.worktrees.BranchChanges(t.Context(), r.repo.Dir, base, r.branch)
	if err != nil || len(got) != 1 || strings.Join(got[0].Files, ",") != "docs/a.md" {
		t.Fatalf("the branch holds %+v beyond its base: %v", got, err)
	}
	if _, err := r.worktrees.BranchChanges(t.Context(), r.repo.Dir, base, "lane/none"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a branch the repository does not hold: %v", err)
	}
	if _, err := r.worktrees.BranchChanges(t.Context(), filepath.Join(r.repo.Dir, "gone"), base, r.branch); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a repository that is gone: %v", err)
	}
}
