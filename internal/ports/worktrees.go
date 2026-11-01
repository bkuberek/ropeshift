package ports

import (
	"context"
	"errors"
)

// Worktree refusals.
var (
	// ErrWorktreeDirty means a removal would lose uncommitted work.
	ErrWorktreeDirty = errors.New("the worktree has uncommitted work")
	// ErrWorktreeConflict means the path or the branch belongs to something else.
	ErrWorktreeConflict = errors.New("the path or branch is taken")
)

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
	// DefaultBranch names the branch a repository's work lands on. It reads an upstream's
	// default first, and a repository with none has its current branch.
	DefaultBranch(ctx context.Context, repository string) (string, error)
}
