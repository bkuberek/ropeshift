package gitcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ropeshift.com/ropeshift/test/gittest"
)

// refused is how long a test waits to be sure a lock stays held. A lock that is free is taken at once, so
// this only bounds a test that expects to be kept waiting.
const refused = 100 * time.Millisecond

func gitIn(repo *gittest.Repo, dir string) Git { return Git{Dir: dir, Env: repo.Env()} }

// stillHeld reports whether the lock of the repository g stands in is held, without waiting for it.
func stillHeld(t *testing.T, g Git) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), refused)
	defer cancel()
	release, err := g.holdWorktrees(ctx)
	if err == nil {
		release()
		return false
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting for the lock gave %v, want the context's deadline", err)
	}
	return true
}

// A repository has one lock, whichever way a caller names it: its own directory, a link to it, or one of its
// linked worktrees, which reach the same common git directory.
func TestOneRepositoryHasOneWorktreeLockWhateverThePath(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "seed\n")
	linked := filepath.Join(t.TempDir(), "linked")
	repo.Git("worktree", "add", "--quiet", "-b", "lane/linked", linked)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo.Dir, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}

	release, err := gitIn(repo, repo.Dir).holdWorktrees(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"its directory": repo.Dir, "a link to it": link, "a linked worktree": linked} {
		if !stillHeld(t, gitIn(repo, dir)) {
			t.Errorf("the lock was free through %s while it was held", name)
		}
	}
	release()
	for name, dir := range map[string]string{"its directory": repo.Dir, "a link to it": link, "a linked worktree": linked} {
		if stillHeld(t, gitIn(repo, dir)) {
			t.Errorf("the lock stayed held through %s after it was given up", name)
		}
	}
}

// Two repositories never wait on each other, because the collision is inside one repository's .git/worktrees.
func TestWorktreeLocksOfTwoRepositoriesAreApart(t *testing.T) {
	first, second := gittest.New(t), gittest.New(t)
	release, err := gitIn(first, first.Dir).holdWorktrees(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if stillHeld(t, gitIn(second, second.Dir)) {
		t.Error("a lock held in one repository kept another repository waiting")
	}
}

// A caller that waits for a held lock stops when its context ends, with the context's error, and does not
// take the lock afterward.
func TestAWorktreeLockWaitEndsWithItsContext(t *testing.T) {
	repo := gittest.New(t)
	git := gitIn(repo, repo.Dir)
	release, err := git.holdWorktrees(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := git.holdWorktrees(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a canceled wait gave %v, want context.Canceled", err)
	}
	release()
	if stillHeld(t, git) {
		t.Error("a canceled wait left the lock held")
	}
}

// Giving a lock up a second time must not give up the hold of the caller that took it next.
func TestGivingAWorktreeLockUpTwiceFreesItOnce(t *testing.T) {
	repo := gittest.New(t)
	git := gitIn(repo, repo.Dir)
	first, err := git.holdWorktrees(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first()
	second, err := git.holdWorktrees(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	first()
	if !stillHeld(t, git) {
		t.Error("a second release freed the hold of the caller that came after")
	}
}

// A directory that is no repository has no lock to take, and the caller hears git's own refusal.
func TestAWorktreeLockOutsideARepositoryFails(t *testing.T) {
	repo := gittest.New(t)
	_, err := gitIn(repo, t.TempDir()).holdWorktrees(t.Context())
	if err == nil {
		t.Fatal("a directory that holds no repository gave a lock")
	}
}
