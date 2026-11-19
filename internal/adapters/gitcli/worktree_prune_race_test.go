package gitcli_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// Git's own auto maintenance runs `git worktree prune` in a process of its own, outside the adapter's lock.
// A prune that looks at the entry `git worktree add` just made under .git/worktrees, before the add writes
// its locked file, decides to remove it, and removes it file by file while the add goes on writing. The add
// then fails with "could not open '.git/worktrees/X/locked' for writing" or "could not find created
// worktree 'X'", or succeeds and leaves the entry without its gitdir, which the next prune removes. A
// create meets each of these and does the whole step again.

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

	// A create that returned is one no later prune removes: the repository lists every worktree, and calls
	// none prunable.
	listed := repo.Git("worktree", "list", "--porcelain")
	if got, want := strings.Count(listed, "worktree "), 1+pruneRacers*pruneRaceOps; got != want || strings.Contains(listed, "\nprunable") {
		t.Errorf("the repository lists %d worktrees, want %d and none prunable:\n%s", got, want, listed)
	}
}

// loseFirstRegistration installs a post-checkout hook that, in the first worktree add that runs it, removes
// the new registration's gitdir file and then runs `git worktree prune` outside the adapter's lock, which
// removes the rest. That is the state a prune in another process leaves when it removes a registration
// file by file while the add is still writing it: the add succeeds, and the worktree's .git file points
// at no registration. It returns the worktree environment that arms the hook, and a check that it ran.
func loseFirstRegistration(t *testing.T, repo *gittest.Repo) (env []string, fired func() bool) {
	t.Helper()
	mark := filepath.Join(t.TempDir(), "fired")
	hooks := filepath.Join(repo.Dir, ".git", "hooks")
	script := `#!/bin/sh
[ -e "$LOSE_REGISTRATION_MARK" ] && exit 0
: > "$LOSE_REGISTRATION_MARK"
rm -f "$(git rev-parse --path-format=absolute --git-dir)/gitdir"
git --git-dir="$(git rev-parse --path-format=absolute --git-common-dir)" worktree prune
`
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return append(repo.Env(), "LOSE_REGISTRATION_MARK="+mark), func() bool {
		_, err := os.Stat(mark)
		return err == nil
	}
}

func TestACreateWhoseRegistrationAPruneRemovedAfterTheAddMakesItAgain(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec ports.WorktreeSpec
	}{
		{name: "detached", spec: ports.WorktreeSpec{Detached: true}},
		{name: "new branch", spec: ports.WorktreeSpec{Branch: "lane/one"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := gittest.New(t)
			repo.Commit("README.txt", "seed\n")
			env, fired := loseFirstRegistration(t, repo)
			worktrees := gitcli.Worktrees{Env: env}
			spec := tc.spec
			spec.Repository, spec.Path = repo.Dir, filepath.Join(t.TempDir(), "lane")

			if _, err := worktrees.Create(t.Context(), spec); err != nil {
				t.Fatalf("create: %v", err)
			}
			if !fired() {
				t.Fatal("no add lost its registration, so the create met no prune")
			}
			repo.Git("worktree", "prune")
			tree, err := worktrees.Inspect(t.Context(), spec.Path)
			if err != nil {
				t.Fatalf("after a prune the worktree the create returned is not a repository: %v", err)
			}
			if tree.Detached != spec.Detached || tree.Branch != spec.Branch {
				t.Errorf("the worktree is %+v, want detached %v on branch %q", tree, spec.Detached, spec.Branch)
			}
		})
	}
}
