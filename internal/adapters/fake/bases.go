package fake

import (
	"context"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// A repository's history here is a graph of named commits: each knows the commits it grew from. It is
// enough to answer what the base of a lane is and whether one commit holds another, with no git.

func tipKey(repository, branch string) string { return repository + "\x00" + branch }

// Grow adds a commit on top of a branch, and returns it. A branch with no commit yet gets its first.
func (w *Worktrees) Grow(repository, branch string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.grown++
	commit := fmt.Sprintf("%040x", w.grown)
	var parents []string
	if tip, ok := w.tips[tipKey(repository, branch)]; ok {
		parents = []string{tip}
	}
	w.history[commit] = parents
	w.tips[tipKey(repository, branch)] = commit
	return commit
}

// Add makes a commit that grew from the given commits and sits on no branch, so a test can build the
// shape of a dependency, a merge, or two lines of work that diverged.
func (w *Worktrees) Add(parents ...string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.grown++
	commit := fmt.Sprintf("%040x", w.grown)
	w.history[commit] = slices.Clone(parents)
	return commit
}

// Point moves a branch to a commit.
func (w *Worktrees) Point(repository, branch, commit string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tips[tipKey(repository, branch)] = commit
}

// FailFetch makes every read of a base fail the way an unreachable remote does, until it is given nil.
// A repository with no remote fetches nothing and is not affected.
func (w *Worktrees) FailFetch(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fetchFail = err
}

// Created lists every worktree that was asked for, in order.
func (w *Worktrees) Created() []ports.WorktreeSpec {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.specs)
}

// BaseTip reads a branch. With a remote it reads the remote-tracking branch, which the fetch brought.
func (w *Worktrees) BaseTip(_ context.Context, repository, branch string) (ports.BaseTip, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ledger.CheckBranch(branch); err != nil {
		return ports.BaseTip{}, fmt.Errorf("%w: %v", ports.ErrBaseUnavailable, err)
	}
	ref := branch
	if w.remote != "" {
		if w.fetchFail != nil {
			return ports.BaseTip{}, fmt.Errorf("%w: fetching %s from %s: %v", ports.ErrBaseUnavailable, branch, w.remote, w.fetchFail)
		}
		ref = w.remote + "/" + branch
	}
	commit, ok := w.tips[tipKey(repository, branch)]
	if !ok {
		return ports.BaseTip{}, fmt.Errorf("%w: no branch %s", ports.ErrBaseUnavailable, ref)
	}
	return ports.BaseTip{Ref: ref, Commit: commit}, nil
}

// Contains walks the commits the second grew from.
func (w *Worktrees) Contains(_ context.Context, _, ancestor, commit string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, name := range []string{ancestor, commit} {
		if _, ok := w.history[name]; !ok {
			return false, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
	}
	seen := map[string]bool{}
	stack := []string{commit}
	for len(stack) > 0 {
		at := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if at == ancestor {
			return true, nil
		}
		if seen[at] {
			continue
		}
		seen[at] = true
		stack = append(stack, w.history[at]...)
	}
	return false, nil
}
