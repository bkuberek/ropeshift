package gitcli_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// A base branch that holds a no-break space, which git takes and a person reads as a plain space, starts no lane,
// however it was stored: its tip is unavailable, naming the branch with an escape in place of the space. A name git
// reads as an option is refused too.
func TestBaseTipRefusesABaseBranchThatHoldsANoBreakSpace(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.md", "one\n")
	branch := "release\u00a0one"
	repo.Git("branch", branch)
	worktrees := gitcli.Worktrees{Env: repo.Env()}
	_, err := worktrees.BaseTip(t.Context(), repo.Dir, branch)
	if !errors.Is(err, ports.ErrBaseUnavailable) || !strings.Contains(err.Error(), `release\u00a0one`) {
		t.Errorf("the tip of %q answers %v, want unavailable with the branch escaped", branch, err)
	}
	if _, err := worktrees.BaseTip(t.Context(), repo.Dir, "-x"); !errors.Is(err, ports.ErrBaseUnavailable) {
		t.Errorf("a branch that reads as an option answers %v", err)
	}
}
