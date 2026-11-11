package ports

import (
	"context"
	"errors"
)

// ErrBaseUnavailable means the branch a lane starts from could not be read: the remote did not answer,
// or it, or the repository, holds no such branch. A caller waits and tries again, and never starts from
// anything else.
var ErrBaseUnavailable = errors.New("the base branch is unavailable")

// BaseTip is where a base branch stands: the ref a person reads it by and the commit it names.
type BaseTip struct {
	// Ref is the remote-tracking branch when the repository has a remote, such as origin/main, and the
	// branch itself when it has none.
	Ref    string
	Commit string
}

// Bases answers what the control plane must know of a repository's git to place a lane: where the
// branch it starts from stands, and whether one commit holds another. It changes nothing a lane can
// see, apart from the remote-tracking refs a fetch brings, and it is apart from Worktrees because the
// control plane has no use for a worktree.
type Bases interface {
	// BaseTip reads the commit a base branch stands at. A repository with a remote fetches the branch
	// first and answers with the remote-tracking commit, whatever the local branch holds. One with no
	// remote answers with the local branch. A failed fetch, or a branch nobody holds, is
	// ErrBaseUnavailable: there is no other answer to give, and never HEAD.
	BaseTip(ctx context.Context, repository, branch string) (BaseTip, error)
	// Contains reports whether ancestor is reachable from commit, and a commit contains itself. A commit
	// the repository does not hold is ErrNotFound.
	Contains(ctx context.Context, repository, ancestor, commit string) (bool, error)
	// DefaultBranch names the branch a repository's work lands on, for a repository no member named a
	// base for. It reads an upstream's default first, and a repository with none has its current branch.
	DefaultBranch(ctx context.Context, repository string) (string, error)
	// Branch reads where an in-flight branch someone else works on stands, for a lane that continues it
	// (ADR-0023). With a remote that holds the branch, it fetches it and answers with the remote-tracking
	// commit; otherwise it answers with the local branch. A branch neither holds is ErrNotFound. It moves
	// no branch.
	Branch(ctx context.Context, repository, branch string) (BaseTip, error)
	// MergeBase finds the commit two commits last shared. Two commits with no common history, or a commit
	// the repository does not hold, is ErrNotFound.
	MergeBase(ctx context.Context, repository, a, b string) (string, error)
}
