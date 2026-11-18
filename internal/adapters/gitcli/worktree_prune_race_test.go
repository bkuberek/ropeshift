package gitcli_test

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// Git's own auto maintenance runs `git worktree prune` in a process of its own, outside the adapter's lock,
// and a prune that runs between `git worktree add` making its entry under .git/worktrees and writing the
// entry's files removes the entry, so the add fails with "could not open '.git/worktrees/X/locked' for
// writing". A create meets that and does the whole step again.

const (
	pruneRacers  = 4
	pruneRaceOps = 30
)

func TestACreateSurvivesAPruneOutsideTheLock(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "seed\n")
	worktrees := gitcli.Worktrees{Env: repo.Env()}
	root := t.TempDir()

	pruning, stop := context.WithCancel(t.Context())
	var pruner sync.WaitGroup
	pruner.Go(func() {
		for pruning.Err() == nil {
			cmd := exec.Command("git", "worktree", "prune", "--expire", "3.months.ago")
			cmd.Dir, cmd.Env = repo.Dir, repo.Env()
			_ = cmd.Run()
		}
	})

	var creators sync.WaitGroup
	for racer := range pruneRacers {
		creators.Go(func() {
			for op := range pruneRaceOps {
				name := fmt.Sprintf("r%d-o%d", racer, op)
				spec := ports.WorktreeSpec{Repository: repo.Dir, Path: filepath.Join(root, name), Detached: true}
				if _, err := worktrees.Create(t.Context(), spec); err != nil {
					t.Errorf("racer %d, create %d: %v", racer, op, err)
					return
				}
			}
		})
	}
	creators.Wait()
	stop()
	pruner.Wait()
}
