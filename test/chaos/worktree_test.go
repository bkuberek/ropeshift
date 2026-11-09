//go:build chaos

package chaos

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/gittest"
)

func worktreeSpec(w *world) childSpec {
	repo := gittest.New(w.t)
	repo.Commit("README.txt", "seed\n")
	return childSpec{
		Role: "worktree-create", Repository: repo.Dir, GitEnv: repo.Env(), Branch: "lane/one",
		Path: filepath.Join(filepath.Dir(repo.Dir), "lanes", "one"),
	}
}

// roleWorktreeCreate asks for one worktree.
func roleWorktreeCreate(c *child) error {
	_, err := gitcli.Worktrees{Env: c.spec.GitEnv}.Create(c.ctx, ports.WorktreeSpec{
		Repository: c.spec.Repository, Path: c.spec.Path, Branch: c.spec.Branch,
	})
	return err
}

func worktreeConverged(w *world, spec childSpec) error {
	got, err := gitcli.Worktrees{Env: spec.GitEnv}.Inspect(w.rig.Ctx, spec.Path)
	if err != nil {
		return contract.Violatedf("no worktree after the retry: %v", err)
	}
	if got.Branch != spec.Branch || !got.Clean {
		return contract.Violatedf("worktree %+v", got)
	}
	return nil
}

func worktreeCase() crashCase {
	return crashCase{
		name:  "worktree create",
		op:    "worktree.create",
		steps: []string{"pruned", "added"},
		prepare: func(w *world) (childSpec, error) {
			return worktreeSpec(w), nil
		},
		settled: worktreeConverged,
	}
}

// checkDeadRegistration is the state a crash leaves when a worktree's directory went
// away and git still lists it. A create must take the branch back.
func checkDeadRegistration(w *world) error {
	spec := worktreeSpec(w)
	worktrees := gitcli.Worktrees{Env: spec.GitEnv}
	request := ports.WorktreeSpec{Repository: spec.Repository, Path: spec.Path, Branch: spec.Branch}
	if _, err := worktrees.Create(w.rig.Ctx, request); err != nil {
		return err
	}
	if err := os.RemoveAll(spec.Path); err != nil {
		return err
	}
	if _, err := worktrees.Create(w.rig.Ctx, request); err != nil {
		return contract.Violatedf("a create after a crash failed: %v", err)
	}
	return worktreeConverged(w, spec)
}

// TestWorktreeRandomKills kills a create at a random moment, which can land while
// git itself runs. Git outlives its parent, so a retry can meet the orphan's lock,
// and it repeats as a reconcile would, until the worktree stands.
func TestWorktreeRandomKills(t *testing.T) {
	kills := min(crashKills(12), 12)
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 11))
	for round := range kills {
		w := newWorld(t, "")
		spec := w.spec(worktreeSpec(w))
		p, err := startChild(spec)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(rng.Int64N(int64(60 * time.Millisecond))))
		p.sigkill()
		if err := retryWorktree(spec); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if err := worktreeConverged(w, spec); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	t.Logf("worktree creates killed: %d", kills)
}

func retryWorktree(spec childSpec) error {
	var last error
	for range 40 {
		code, signaled, stderr, err := runToEnd(spec)
		if err != nil {
			return err
		}
		if !signaled && code == exitDone {
			return nil
		}
		last = fmt.Errorf("exit %d: %s", code, stderr)
		time.Sleep(50 * time.Millisecond)
	}
	return errors.Join(errors.New("the retry never converged"), last)
}
