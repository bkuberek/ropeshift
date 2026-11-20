package gitcli_test

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// A base branch a repository was registered with before branch names grew stricter, one that holds a no-break space,
// is still read: git takes the name, and a lane starts from it. A name git reads as an option is still refused.
func TestBaseTipReadsAStoredBranchThatHoldsANoBreakSpace(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.md", "one\n")
	branch := "release one"
	repo.Git("branch", branch)
	worktrees := gitcli.Worktrees{Env: repo.Env()}
	tip, err := worktrees.BaseTip(t.Context(), repo.Dir, branch)
	if err != nil || tip.Ref != branch || tip.Commit == "" {
		t.Fatalf("the tip of %q is %+v (%v)", branch, tip, err)
	}
	if _, err := worktrees.BaseTip(t.Context(), repo.Dir, "-x"); !errors.Is(err, ports.ErrBaseUnavailable) {
		t.Errorf("a branch that reads as an option answers %v", err)
	}
}
