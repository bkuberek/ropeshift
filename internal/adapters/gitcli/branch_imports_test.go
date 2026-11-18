package gitcli_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// A plan approval reads the merge a consolidation made from the repository's own history: the branch's commits
// since it began, the merge among them with the workspace first and the design tip second, and the paths each
// parent holds.
func TestBranchCommitsListTheImportMergeWithItsParents(t *testing.T) {
	r := newImportRig(t)
	made, err := r.worktrees.ImportHistory(t.Context(), r.path, r.request())
	if err != nil {
		t.Fatal(err)
	}
	r.inWorktree("-c", "user.name=lane", "-c", "user.email=lane@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "docs: merge the agent instructions")
	head := r.inWorktree("rev-parse", "HEAD")
	commits, err := r.worktrees.BranchCommits(t.Context(), r.workspace.Dir, r.base, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].Hash != head || commits[0].Subject != "docs: merge the agent instructions" {
		t.Fatalf("the branch's commits are %+v", commits)
	}
	if merge := commits[1]; merge.Hash != made.Hash || merge.Subject != importSubject || !slices.Equal(merge.Parents, []string{r.base, r.tip}) || len(merge.Files) != 0 {
		t.Errorf("the merge reads %+v, want %s with parents %s and %s", merge, made.Hash, r.base, r.tip)
	}
	// With no base, the newest commits of the branch's own line, and none of the history the merge brought in.
	line, err := r.worktrees.BranchCommits(t.Context(), r.workspace.Dir, "", head)
	if err != nil || len(line) != 5 || line[1].Hash != made.Hash || len(line[4].Parents) != 0 {
		t.Errorf("the branch's line reads %+v: %v", line, err)
	}
	theirs, err := r.worktrees.CommitPaths(t.Context(), r.workspace.Dir, r.tip)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".gitignore", "AGENTS.md", "README.txt", "docs/vision.md", "poc/go.mod", "poc/main.go"}; !slices.Equal(theirs, want) {
		t.Errorf("the design tip holds %v, want %v", theirs, want)
	}
	if ours, err := r.worktrees.CommitPaths(t.Context(), r.workspace.Dir, r.base); err != nil || !slices.Equal(ours, []string{".gitignore", "README.txt", "workspace-only.md"}) {
		t.Errorf("the workspace held %v: %v", ours, err)
	}
}

func TestBranchCommitsRefuseACommitTheRepositoryDoesNotHold(t *testing.T) {
	r := newImportRig(t)
	missing := strings.Repeat("e", 40)
	for name, read := range map[string]func() error{
		"head": func() error {
			_, err := r.worktrees.BranchCommits(t.Context(), r.workspace.Dir, r.base, missing)
			return err
		},
		"base": func() error {
			_, err := r.worktrees.BranchCommits(t.Context(), r.workspace.Dir, missing, r.base)
			return err
		},
		"option": func() error {
			_, err := r.worktrees.BranchCommits(t.Context(), r.workspace.Dir, "", "--all")
			return err
		},
		"paths":   func() error { _, err := r.worktrees.CommitPaths(t.Context(), r.workspace.Dir, missing); return err },
		"pathopt": func() error { _, err := r.worktrees.CommitPaths(t.Context(), r.workspace.Dir, "--all"); return err },
	} {
		if err := read(); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("%s: %v, want not found", name, err)
		}
	}
}
