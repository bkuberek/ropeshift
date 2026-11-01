package fake

import (
	"context"
	"fmt"
	"sync"

	"ropeshift.dev/ropeshift/internal/ports"
)

// Worktrees keeps worktrees in memory, so the core runs with no git.
type Worktrees struct {
	mu       sync.Mutex
	byPath   map[string]ports.Worktree
	branches map[string]string
}

// NewWorktrees returns an empty store.
func NewWorktrees() *Worktrees {
	return &Worktrees{byPath: map[string]ports.Worktree{}, branches: map[string]string{}}
}

// Dirty stands for an edit in a worktree.
func (w *Worktrees) Dirty(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entry := w.byPath[path]
	entry.Clean = false
	w.byPath[path] = entry
}

// Create makes a worktree, or returns the one that matches.
func (w *Worktrees) Create(_ context.Context, spec ports.WorktreeSpec) (ports.Worktree, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if existing, ok := w.byPath[spec.Path]; ok {
		if existing.Branch == spec.Branch {
			return existing, nil
		}
		return ports.Worktree{}, fmt.Errorf("%w: %s holds %s", ports.ErrWorktreeConflict, spec.Path, existing.Branch)
	}
	if holder, taken := w.branches[spec.Branch]; taken {
		return ports.Worktree{}, fmt.Errorf("%w: %s is checked out at %s", ports.ErrWorktreeConflict, spec.Branch, holder)
	}
	created := ports.Worktree{Path: spec.Path, Branch: spec.Branch, Head: "fake-head", Clean: true}
	w.byPath[spec.Path] = created
	w.branches[spec.Branch] = spec.Path
	return created, nil
}

// Inspect reads a worktree.
func (w *Worktrees) Inspect(_ context.Context, path string) (ports.Worktree, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entry, ok := w.byPath[path]
	if !ok {
		return ports.Worktree{}, fmt.Errorf("worktree %s: %w", path, ports.ErrNotFound)
	}
	return entry, nil
}

// Remove deletes a clean worktree.
func (w *Worktrees) Remove(_ context.Context, _, path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	entry, ok := w.byPath[path]
	if !ok {
		return nil
	}
	if !entry.Clean {
		return fmt.Errorf("%w: %s", ports.ErrWorktreeDirty, path)
	}
	delete(w.byPath, path)
	delete(w.branches, entry.Branch)
	return nil
}

// DefaultBranch names the branch every fake repository lands on.
func (w *Worktrees) DefaultBranch(context.Context, string) (string, error) { return "main", nil }

var _ ports.Worktrees = (*Worktrees)(nil)
