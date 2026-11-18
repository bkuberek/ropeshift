package fake

import (
	"context"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/ports"
)

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

// CommitPaths reads the paths Hold gave a commit. A commit the history does not hold is ErrNotFound.
func (w *Worktrees) CommitPaths(_ context.Context, _, commit string) ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.history[commit]; !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, commit)
	}
	return slices.Clone(w.trees[commit]), nil
}

var _ ports.BranchImports = (*Worktrees)(nil)
