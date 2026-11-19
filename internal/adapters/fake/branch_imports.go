package fake

import (
	"context"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/ports"
)

// At makes a commit with a name of the test's choosing that grew from the given commits and sits on no branch, so
// a commit a lane reported by a name joins the history.
func (w *Worktrees) At(commit string, parents ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.history[commit] = slices.Clone(parents)
}

// Name gives a commit a subject, which BranchCommits reports.
func (w *Worktrees) Name(commit, subject string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.subjects == nil {
		w.subjects = map[string]string{}
	}
	w.subjects[commit] = subject
}

// Hold says which paths a commit holds, which CommitPaths reports.
func (w *Worktrees) Hold(commit string, paths ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.trees == nil {
		w.trees = map[string][]string{}
	}
	w.trees[commit] = slices.Sorted(slices.Values(paths))
}

// BranchCommits walks head's first parents until it meets a commit base holds, newest first. An empty base walks
// to the first commit.
func (w *Worktrees) BranchCommits(_ context.Context, _, base, head string) ([]ports.Commit, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, name := range []string{base, head} {
		if _, ok := w.history[name]; name != "" && !ok {
			return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
	}
	held := map[string]bool{}
	for stack := []string{base}; base != "" && len(stack) > 0; {
		at := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !held[at] {
			held[at] = true
			stack = append(stack, w.history[at]...)
		}
	}
	var commits []ports.Commit
	for at := head; at != "" && !held[at]; {
		parents := w.history[at]
		commits = append(commits, ports.Commit{Hash: at, Subject: w.subjects[at], Parents: slices.Clone(parents)})
		at = ""
		if len(parents) != 0 {
			at = parents[0]
		}
	}
	return commits, nil
}

// CommitPaths reads the paths Hold gave a commit, at most limit of them. A commit the history does not hold is
// ErrNotFound.
func (w *Worktrees) CommitPaths(_ context.Context, _, commit string, limit int) ([]string, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.history[commit]; !ok {
		return nil, false, fmt.Errorf("%w: %s", ports.ErrNotFound, commit)
	}
	paths := w.trees[commit]
	if len(paths) > limit {
		return slices.Clone(paths[:limit]), true, nil
	}
	return slices.Clone(paths), false, nil
}

// FirstParentRoot walks a commit's first parents to the one with none.
func (w *Worktrees) FirstParentRoot(_ context.Context, _, commit string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.history[commit]; !ok {
		return "", fmt.Errorf("%w: %s", ports.ErrNotFound, commit)
	}
	at := commit
	for len(w.history[at]) != 0 {
		at = w.history[at][0]
	}
	return at, nil
}

var _ ports.BranchImports = (*Worktrees)(nil)
