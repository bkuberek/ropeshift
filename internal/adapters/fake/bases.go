package fake

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
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

// Touch adds a commit on top of a branch that changes one file, and returns it.
func (w *Worktrees) Touch(repository, branch, file string) string {
	commit := w.Grow(repository, branch)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.touched[commit] = append(w.touched[commit], file)
	return commit
}

// Branches lists a repository's branches: the ones its history names and the ones a worktree holds.
func (w *Worktrees) Branches(repository string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	seen := map[string]bool{}
	for key := range w.tips {
		if name, ok := strings.CutPrefix(key, repository+"\x00"); ok {
			seen[name] = true
		}
	}
	for name := range w.branches {
		seen[name] = true
	}
	return slices.Sorted(maps.Keys(seen))
}

// Diff walks the commits head grew from since base and lists the files they touched. A fake commit holds
// no text, so the patch only names each file.
func (w *Worktrees) Diff(_ context.Context, _, base, head string) (ports.Diff, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, name := range []string{base, head} {
		if _, ok := w.history[name]; !ok || strings.HasPrefix(name, "-") {
			return ports.Diff{}, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
	}
	var files []string
	seen := map[string]bool{}
	stack := []string{head}
	for len(stack) > 0 {
		at := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if at == base || seen[at] {
			continue
		}
		seen[at] = true
		for _, file := range w.touched[at] {
			if !slices.Contains(files, file) {
				files = append(files, file)
			}
		}
		stack = append(stack, w.history[at]...)
	}
	var patch strings.Builder
	for _, file := range files {
		fmt.Fprintf(&patch, "diff --git a/%s b/%s\n", file, file)
	}
	return ports.Diff{Patch: patch.String(), Files: files}, nil
}

// ChangeSize counts the files the commits head grew from since base touched. A fake commit holds no text, so
// each file counts one line added, as one written by Touch would.
func (w *Worktrees) ChangeSize(ctx context.Context, path, base, head string) (ledger.ChangeSize, error) {
	diff, err := w.Diff(ctx, path, base, head)
	if err != nil {
		return ledger.ChangeSize{}, err
	}
	files := int64(len(diff.Files))
	return ledger.ChangeSize{Files: files, Added: files}, nil
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

// Branch reads a branch a lane continues. With a remote it reads the remote's, which the fetch brought,
// and a fetch that fails falls back to the branch this machine holds. A branch nobody grew is ErrNotFound.
func (w *Worktrees) Branch(_ context.Context, repository, branch string) (ports.BaseTip, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	commit, ok := w.tips[tipKey(repository, branch)]
	if !ok {
		return ports.BaseTip{}, fmt.Errorf("%w: no branch %s", ports.ErrNotFound, branch)
	}
	ref := branch
	if w.remote != "" && w.fetchFail == nil {
		ref = w.remote + "/" + branch
	}
	return ports.BaseTip{Ref: ref, Commit: commit}, nil
}

// MergeBase walks what each commit grew from and answers with the first commit b's history meets that a's
// holds, nearest first.
func (w *Worktrees) MergeBase(_ context.Context, _, a, b string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, name := range []string{a, b} {
		if _, ok := w.history[name]; !ok {
			return "", fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
	}
	held := map[string]bool{}
	for stack := []string{a}; len(stack) > 0; {
		at := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !held[at] {
			held[at] = true
			stack = append(stack, w.history[at]...)
		}
	}
	seen := map[string]bool{}
	for queue := []string{b}; len(queue) > 0; {
		at := queue[0]
		queue = queue[1:]
		if held[at] {
			return at, nil
		}
		if !seen[at] {
			seen[at] = true
			queue = append(queue, w.history[at]...)
		}
	}
	return "", fmt.Errorf("%w: %s and %s share no history", ports.ErrNotFound, a, b)
}

// Changes walks the commits head grew from since base, as Diff does. A fake commit holds no text, so a file
// that a commit base holds already touched is changed, and one it never touched is added; nothing here is
// ever moved or deleted.
func (w *Worktrees) Changes(ctx context.Context, repository, base, head string) ([]planning.FileChange, error) {
	diff, err := w.Diff(ctx, repository, base, head)
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	before := map[string]bool{}
	seen := map[string]bool{}
	for stack := []string{base}; len(stack) > 0; {
		at := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[at] {
			continue
		}
		seen[at] = true
		for _, file := range w.touched[at] {
			before[file] = true
		}
		stack = append(stack, w.history[at]...)
	}
	changes := []planning.FileChange{}
	for _, file := range diff.Files {
		status := planning.ChangeAdded
		if before[file] {
			status = planning.ChangeChanged
		}
		changes = append(changes, planning.FileChange{Status: status, Path: file})
	}
	slices.SortFunc(changes, func(a, b planning.FileChange) int { return strings.Compare(a.Path, b.Path) })
	return changes, nil
}

var _ ports.Bases = (*Worktrees)(nil)
