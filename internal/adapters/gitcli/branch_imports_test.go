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
	theirs, truncated, err := r.worktrees.CommitPaths(t.Context(), r.workspace.Dir, r.tip, 100)
	if err != nil || truncated {
		t.Fatalf("the design tip's paths: cut short %v, %v", truncated, err)
	}
	if want := []string{".gitignore", "AGENTS.md", "README.txt", "docs/vision.md", "poc/go.mod", "poc/main.go"}; !slices.Equal(theirs, want) {
		t.Errorf("the design tip holds %v, want %v", theirs, want)
	}
	if ours, _, err := r.worktrees.CommitPaths(t.Context(), r.workspace.Dir, r.base, 100); err != nil || !slices.Equal(ours, []string{".gitignore", "README.txt", "workspace-only.md"}) {
		t.Errorf("the workspace held %v: %v", ours, err)
	}
	if root, err := r.worktrees.FirstParentRoot(t.Context(), r.workspace.Dir, r.tip); err != nil || root != r.root {
		t.Errorf("the design tip's first-parent root is %s, want %s: %v", root, r.root, err)
	}
	if root, err := r.worktrees.FirstParentRoot(t.Context(), r.workspace.Dir, made.Hash); err != nil || root != r.workspace.Roots("main")[0] {
		t.Errorf("the merge's first-parent root is %s: %v", root, err)
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
		"paths": func() error {
			_, _, err := r.worktrees.CommitPaths(t.Context(), r.workspace.Dir, missing, 10)
			return err
		},
		"pathopt": func() error {
			_, _, err := r.worktrees.CommitPaths(t.Context(), r.workspace.Dir, "--all", 10)
			return err
		},
		"root": func() error { _, err := r.worktrees.FirstParentRoot(t.Context(), r.workspace.Dir, missing); return err },
	} {
		if err := read(); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("%s: %v, want not found", name, err)
		}
	}
}

// A branch whose session made many commits after the import still lists the import, which is its oldest commit.
func TestBranchCommitsReachAnImportUnderALongSession(t *testing.T) {
	r := newImportRig(t)
	made, err := r.worktrees.ImportHistory(t.Context(), r.path, r.request())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 60 {
		r.inWorktree("-c", "user.name=lane", "-c", "user.email=lane@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "work "+strings.Repeat("x", i%3+1))
	}
	head := r.inWorktree("rev-parse", "HEAD")
	for _, base := range []string{r.base, ""} {
		commits, err := r.worktrees.BranchCommits(t.Context(), r.workspace.Dir, base, head)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(commits, func(commit ports.Commit) bool { return commit.Hash == made.Hash }) {
			t.Errorf("from base %q the branch's %d commits leave out the import", base, len(commits))
		}
	}
}

// A tree larger than the limit lists the limit, and says it left some out.
func TestCommitPathsCutALargeTreeShort(t *testing.T) {
	r := newImportRig(t)
	paths, truncated, err := r.worktrees.CommitPaths(t.Context(), r.workspace.Dir, r.base, 2)
	if err != nil || !truncated || len(paths) != 2 {
		t.Errorf("a tree of three paths at a limit of two reads %v, cut short %v: %v", paths, truncated, err)
	}
	paths, truncated, err = r.worktrees.CommitPaths(t.Context(), r.workspace.Dir, r.base, 3)
	if err != nil || truncated || len(paths) != 3 {
		t.Errorf("a tree of three paths at a limit of three reads %v, cut short %v: %v", paths, truncated, err)
	}
}
