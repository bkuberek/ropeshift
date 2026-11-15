package gitcli

import (
	"context"
	"sync"
)

// worktreeLocks holds the lock of each repository whose worktrees this process changes, keyed by the
// repository's common git directory. A lock is a channel with one slot, which a caller can stop waiting for
// when its context ends, and an entry stays for the life of the process, one for each repository.
//
// Git keeps one registration for each linked worktree under the repository's common git directory, in
// .git/worktrees, and `git worktree add`, `remove` and `prune` read every one of them while they write their
// own. A command that meets another's half-made or half-removed registration fails, with "failed to read
// .git/worktrees/X/commondir", "Invalid path", or "Directory not empty". Lanes that start and archive in one
// repository at the same time do exactly that, so every command here that adds, removes, or prunes a
// registration, or that reads them all as a step of doing so, takes the repository's lock first, and holds it
// for that one command. A merge, a push, or a fetch between two of them holds nothing.
//
// The lock is this process's: it keeps the daemon's lanes from colliding with each other. A person running
// git by hand in the repository is not held back by it.
var worktreeLocks sync.Map

// worktreeLockKey names the lock of the repository g stands in. Every path to a repository, through a link or
// from one of its linked worktrees, has the same common git directory, so every path takes the same lock.
func (g Git) worktreeLockKey(ctx context.Context) (string, error) {
	dir, err := g.Text(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return canonical(dir), nil
}

// holdWorktrees waits for the lock on the repository g stands in, and returns the function that gives it up.
// Giving it up twice gives it up once. It fails when ctx ends first, and when git cannot say which repository
// this is, because a command that cannot be placed in one cannot be kept from colliding in it.
func (g Git) holdWorktrees(ctx context.Context) (release func(), err error) {
	key, err := g.worktreeLockKey(ctx)
	if err != nil {
		return nil, err
	}
	slot, _ := worktreeLocks.LoadOrStore(key, make(chan struct{}, 1))
	held := slot.(chan struct{})
	select {
	case held <- struct{}{}:
		return sync.OnceFunc(func() { <-held }), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// changeWorktrees runs `git worktree` with these arguments under the repository's lock. It is how a
// registration is added, removed, or pruned.
func (g Git) changeWorktrees(ctx context.Context, args ...string) error {
	release, err := g.holdWorktrees(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = g.Output(ctx, append([]string{"worktree"}, args...)...)
	return err
}
