package gitcli_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// TestChangeSizeCountsWhatABranchChangedSinceItsBase holds FR-OBS-13 on a real repository: an edited
// file's lines, a binary file as a file with no lines, and a moved file as one file.
func TestChangeSizeCountsWhatABranchChangedSinceItsBase(t *testing.T) {
	repo := gittest.New(t)
	lines := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta", "iota", "kappa"}
	base := repo.Commit("README.md", strings.Join(lines, "\n")+"\n")
	lines[1] = "BETA"
	repo.Commit("README.md", strings.Join(append(lines, "lambda"), "\n")+"\n")
	repo.Commit("logo.png", "\x89PNG\x00\x01\x02\x03")
	repo.Rename("README.md", "docs/README.md")
	head := repo.Git("rev-parse", "HEAD")
	worktrees := gitcli.Worktrees{Env: repo.Env()}
	got, err := worktrees.ChangeSize(t.Context(), repo.Dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if want := (ledger.ChangeSize{Files: 2, Added: 2, Removed: 1}); got != want {
		t.Errorf("change size %+v, want %+v", got, want)
	}
	if got, err := worktrees.ChangeSize(t.Context(), repo.Dir, head, head); err != nil || got != (ledger.ChangeSize{}) {
		t.Errorf("a commit against itself: %+v, %v", got, err)
	}
	for _, name := range []string{strings.Repeat("a", 40), "--output=x"} {
		if _, err := worktrees.ChangeSize(t.Context(), repo.Dir, name, head); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("a base of %q: %v", name, err)
		}
	}
}
