package ports

import (
	"context"
	"errors"

	"ropeshift.com/ropeshift/internal/lanes"
)

// Worktree refusals.
var (
	// ErrWorktreeDirty means a removal would lose uncommitted work.
	ErrWorktreeDirty = errors.New("the worktree has uncommitted work")
	// ErrWorktreeConflict means the path or the branch belongs to something else.
	ErrWorktreeConflict = errors.New("the path or branch is taken")
	// ErrBaseUnavailable means the branch a lane starts from could not be read: the remote did not
	// answer, or it, or the repository, holds no such branch. A caller waits and tries again, and
	// never starts from anything else.
	ErrBaseUnavailable = errors.New("the base branch is unavailable")
)

// BaseTip is where a base branch stands: the ref a person reads it by and the commit it names.
type BaseTip struct {
	// Ref is the remote-tracking branch when the repository has a remote, such as origin/main, and the
	// branch itself when it has none.
	Ref    string
	Commit string
}

// WorktreeSpec asks for one lane's worktree. An empty Base starts the branch at the
// repository's HEAD.
type WorktreeSpec struct {
	Repository string
	Path       string
	Branch     string
	Base       string
}

// Worktree is one worktree as it stands.
type Worktree struct {
	Path   string
	Branch string
	Head   string
	// Clean is false when a tracked or an untracked file differs from HEAD.
	Clean bool
}

// Worktrees manages git worktrees.
type Worktrees interface {
	// Create makes a worktree, or returns the one that already matches, so a retry
	// after a crash converges. A path or branch held by something else is
	// ErrWorktreeConflict.
	Create(ctx context.Context, spec WorktreeSpec) (Worktree, error)
	// Inspect reads a worktree, or returns ErrNotFound.
	Inspect(ctx context.Context, path string) (Worktree, error)
	// Remove deletes a clean worktree and keeps its branch. Uncommitted work is
	// ErrWorktreeDirty, and a worktree already gone is done.
	Remove(ctx context.Context, repository, path string) error
	// Discard deletes a worktree whatever it holds, and keeps its branch. A worktree
	// already gone is done.
	Discard(ctx context.Context, repository, path string) error
	// Survey reads what a stop must account for, and changes nothing a session can see. An
	// absent worktree is ErrNotFound.
	Survey(ctx context.Context, path string) (lanes.WorktreeFindings, error)
	// Snapshot records the worktree as it stands in a commit under ref, untracked files
	// included, and leaves the lane's index and branch alone. A snapshot of what the ref
	// already holds is that commit, and an earlier snapshot stays reachable from a later
	// one, so a repeat loses nothing.
	Snapshot(ctx context.Context, path, ref string) (string, error)
	// Push sends one ref to a remote under the same name, and never forces.
	Push(ctx context.Context, path, remote, ref string) error
	// Fetch brings refs a remote holds into a repository under the same names, so a lane can
	// go on from work another machine pushed. A repository with no remote, and a ref the remote
	// does not hold, are done: a worktree then starts from what this machine has.
	Fetch(ctx context.Context, repository string, refs []string) error
	// BaseTip reads the commit a base branch stands at. A repository with a remote fetches the branch
	// first and answers with the remote-tracking commit, whatever the local branch holds. One with no
	// remote answers with the local branch. A failed fetch, or a branch nobody holds, is
	// ErrBaseUnavailable: there is no other answer to give, and never HEAD.
	BaseTip(ctx context.Context, repository, branch string) (BaseTip, error)
	// Contains reports whether ancestor is reachable from commit, and a commit contains itself. A commit
	// the repository does not hold is ErrNotFound.
	Contains(ctx context.Context, repository, ancestor, commit string) (bool, error)
	// DefaultBranch names the branch a repository's work lands on. It reads an upstream's
	// default first, and a repository with none has its current branch.
	DefaultBranch(ctx context.Context, repository string) (string, error)
	// WriteContext leaves a pack's text in a directory git does not see, and returns its path. It
	// repeats safely, and a survey, a snapshot, and a removal all read the worktree as if it were
	// not there.
	WriteContext(ctx context.Context, path, text string) (string, error)
}
