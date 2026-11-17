package fake_test

import (
	"errors"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ports"
)

// The fake commits files by the rules the port states: a file only when none is there, a line only when the file
// holds none like it, one commit for what changed, and none for a repeat.
func TestTheFakeCommitsFilesByTheRulesOfThePort(t *testing.T) {
	t.Parallel()
	worktrees := fake.NewWorktrees()
	worktrees.SeedFile("/lanes/one", "Makefile", "all:\n")
	worktrees.SeedFile("/lanes/one", ".gitignore", "node_modules/\nrepos/")
	files := []ports.TreeFile{
		{Path: "plan/repositories.md", Text: "manifest\n"},
		{Path: "Makefile", Text: "setup:\n"},
		{Path: ".gitignore", Lines: []string{"repos/", "/.ropeshift/worktrees/"}},
	}
	made, err := worktrees.CommitFiles(t.Context(), "/lanes/one", files, "chore: add the workspace files")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".gitignore", "plan/repositories.md"}; !slices.Equal(made.Files, want) || made.Hash == "" {
		t.Errorf("the commit holds %+v, want files %v", made, want)
	}
	if text, _ := worktrees.FileText("/lanes/one", "Makefile"); text != "all:\n" {
		t.Errorf("an existing Makefile reads %q", text)
	}
	if text, _ := worktrees.FileText("/lanes/one", ".gitignore"); text != "node_modules/\nrepos/\n/.ropeshift/worktrees/\n" {
		t.Errorf("the ignore file reads %q", text)
	}
	again, err := worktrees.CommitFiles(t.Context(), "/lanes/one", files, "chore: add the workspace files")
	if err != nil || again.Hash != "" {
		t.Errorf("a repeat made %+v: %v", again, err)
	}
	if got, err := worktrees.Commits(t.Context(), "/lanes/one", "base"); err != nil || len(got) != 1 || got[0].Hash != made.Hash {
		t.Errorf("the worktree holds %+v: %v", got, err)
	}
	if none, err := worktrees.Commits(t.Context(), "/lanes/two", ""); err != nil || len(none) != 0 {
		t.Errorf("a worktree with no commit holds %+v: %v", none, err)
	}
	refused := errors.New("the disk is full")
	worktrees.FailCommits(refused)
	if _, err := worktrees.CommitFiles(t.Context(), "/lanes/two", files, "x"); !errors.Is(err, refused) {
		t.Errorf("a commit that fails: %v", err)
	}
}
