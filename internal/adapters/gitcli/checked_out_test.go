package gitcli

import (
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// CheckedOut names the linked worktree that has a branch checked out, wherever it stands, so a lane that began
// in a worktree keeps it when the root a profile names moves. It names nothing for a branch no worktree holds,
// for the repository's own working tree, and for a registration whose directory is gone.
func TestCheckedOutFindsTheWorktreeAStandingBranchLivesIn(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "seed\n")
	w := Worktrees{Env: repo.Env()}
	var _ ports.BranchTrees = w
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(root, "repository-lanes", "lane-1")
	if _, err := w.Create(t.Context(), ports.WorktreeSpec{Repository: repo.Dir, Path: old, Branch: "lane/lane-1"}); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(root, "gone", "lane-2")
	if _, err := w.Create(t.Context(), ports.WorktreeSpec{Repository: repo.Dir, Path: gone, Branch: "lane/lane-2"}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	repo.Git("branch", "lane/lane-3")

	for name, tt := range map[string]struct{ branch, want string }{
		"a branch a linked worktree holds":          {"lane/lane-1", old},
		"a branch no worktree holds":                {"lane/lane-3", ""},
		"the branch the repository's own tree has":  {"main", ""},
		"a branch whose worktree directory is gone": {"lane/lane-2", ""},
		"a branch that does not exist":              {"lane/nothing", ""},
	} {
		got, err := w.CheckedOut(t.Context(), repo.Dir, tt.branch)
		if err != nil || got != tt.want {
			t.Errorf("%s: %q (%v), want %q", name, got, err, tt.want)
		}
	}
}
