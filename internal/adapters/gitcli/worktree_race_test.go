package gitcli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// Lanes start and archive in one repository at the same time, and git does not keep that safe by itself:
// `git worktree add`, `remove` and `prune` read and write the entries under .git/worktrees, so one that
// meets another's half-made or half-removed entry fails with "failed to read .git/worktrees/X/commondir",
// "Invalid path" or "Directory not empty". The adapter takes these one at a time within a repository.

const (
	churners       = 20
	churnerRounds  = 10
	churnerMethods = 3
)

// churn makes one worktree and ends it in one of the three ways a lane's worktree ends: removed clean,
// discarded with whatever it holds, or gone from disk before the registration is cleared, which is the
// prune. A create prunes first as well, so every round meets the others' registrations.
func churn(worktrees gitcli.Worktrees, t *testing.T, repository, root string, worker, round int) error {
	name := fmt.Sprintf("w%d-r%d", worker, round)
	spec := ports.WorktreeSpec{Repository: repository, Path: filepath.Join(root, name), Branch: "lane/" + name}
	method := (worker + round) % churnerMethods
	spec.Detached = method == 1
	if _, err := worktrees.Create(t.Context(), spec); err != nil {
		return fmt.Errorf("create: %w", err)
	}
	var err error
	switch method {
	case 0:
		err = worktrees.Remove(t.Context(), repository, spec.Path)
	case 1:
		err = worktrees.Discard(t.Context(), repository, spec.Path)
	default:
		if err = os.RemoveAll(spec.Path); err == nil {
			err = worktrees.Remove(t.Context(), repository, spec.Path)
		}
	}
	if err != nil {
		return fmt.Errorf("end: %w", err)
	}
	return nil
}

func TestWorktreeChangesInOneRepositoryDoNotCollide(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "seed\n")
	worktrees := gitcli.Worktrees{Env: repo.Env()}
	root := t.TempDir()

	var wg sync.WaitGroup
	for worker := range churners {
		wg.Go(func() {
			for round := range churnerRounds {
				if err := churn(worktrees, t, repo.Dir, root, worker, round); err != nil {
					t.Errorf("worker %d, round %d: %v", worker, round, err)
					return
				}
			}
		})
	}
	wg.Wait()

	if listed := repo.Git("worktree", "list", "--porcelain"); strings.Count(listed, "worktree ") != 1 {
		t.Errorf("every worktree was ended, yet the repository lists:\n%s", listed)
	}
}
