package fake

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// ErrPushRefused stands for a remote that takes nothing.
var ErrPushRefused = errors.New("the remote refused the push")

// Worktrees keeps worktrees in memory, so the core runs with no git.
type Worktrees struct {
	mu        sync.Mutex
	byPath    map[string]ports.Worktree
	branches  map[string]string
	remote    string
	ahead     map[string]int64
	refuse    bool
	survey    error
	pushed    []string
	fetched   []string
	snapshots map[string]string
	contexts  map[string][]string
	context   error
	// history is the commits a repository holds, each with the commits it grew from. tips maps a
	// repository and a branch to its commit. specs lists every worktree asked for, in order.
	history   map[string][]string
	tips      map[string]string
	fetchFail error
	specs     []ports.WorktreeSpec
	grown     int
}

// NewWorktrees returns an empty store.
func NewWorktrees() *Worktrees {
	return &Worktrees{
		byPath: map[string]ports.Worktree{}, branches: map[string]string{}, ahead: map[string]int64{},
		snapshots: map[string]string{}, contexts: map[string][]string{},
		history: map[string][]string{}, tips: map[string]string{},
	}
}

// FailContext makes every WriteContext fail, until it is given nil.
func (w *Worktrees) FailContext(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.context = err
}

// Context reads the last pack text left in a worktree.
func (w *Worktrees) Context(path string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	writes := w.contexts[path]
	if len(writes) == 0 {
		return ""
	}
	return writes[len(writes)-1]
}

// ContextWrites lists every pack text left in a worktree, oldest first.
func (w *Worktrees) ContextWrites(path string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.contexts[path])
}

// WriteContext keeps the text, and git sees none of it, so a survey reads no change.
func (w *Worktrees) WriteContext(_ context.Context, path, text string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.context != nil {
		return "", w.context
	}
	w.contexts[path] = append(w.contexts[path], text)
	return filepath.Join(path, lanes.PackFile), nil
}

// UseRemote gives every worktree a remote.
func (w *Worktrees) UseRemote(name string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.remote = name
}

// RefusePushes makes every push fail, until it is given false.
func (w *Worktrees) RefusePushes(on bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refuse = on
}

// FailSurvey makes every Survey an incomplete read, until it is given nil.
func (w *Worktrees) FailSurvey(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.survey = err
}

// Commit stands for a commit only this branch holds.
func (w *Worktrees) Commit(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ahead[path]++
}

// Fetch keeps which refs a repository asked for.
func (w *Worktrees) Fetch(_ context.Context, _ string, refs []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fetched = append(w.fetched, refs...)
	return nil
}

// Fetched lists every ref a fetch asked for, in order.
func (w *Worktrees) Fetched() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string{}, w.fetched...)
}

// Pushed lists every ref a push took, in order.
func (w *Worktrees) Pushed() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string{}, w.pushed...)
}

// Snapshots maps each ref to the worktree it was taken from.
func (w *Worktrees) Snapshots() map[string]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]string, len(w.snapshots))
	for ref, path := range w.snapshots {
		out[ref] = path
	}
	return out
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
	w.specs = append(w.specs, spec)
	head := "fake-head"
	if spec.Base != "" {
		head = spec.Base
	}
	created := ports.Worktree{Path: spec.Path, Branch: spec.Branch, Head: head, Clean: true}
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

// Discard deletes a worktree whatever it holds.
func (w *Worktrees) Discard(_ context.Context, _, path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if entry, ok := w.byPath[path]; ok {
		delete(w.byPath, path)
		delete(w.branches, entry.Branch)
	}
	return nil
}

// DirtyPath is the file a dirty worktree here says changed.
const DirtyPath = "changed.txt"

// Survey reads what a stop must account for.
func (w *Worktrees) Survey(_ context.Context, path string) (lanes.WorktreeFindings, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.survey != nil {
		return lanes.WorktreeFindings{}, w.survey
	}
	entry, ok := w.byPath[path]
	if !ok {
		return lanes.WorktreeFindings{}, fmt.Errorf("worktree %s: %w", path, ports.ErrNotFound)
	}
	found := lanes.WorktreeFindings{
		Branch: entry.Branch, Head: entry.Head, Dirty: !entry.Clean, Remote: w.remote, Ahead: w.ahead[path],
	}
	if found.Dirty {
		found.DirtyPaths = []string{DirtyPath}
	}
	return found, nil
}

// Snapshot records which worktree a ref holds.
func (w *Worktrees) Snapshot(_ context.Context, path, ref string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.byPath[path]; !ok {
		return "", fmt.Errorf("worktree %s: %w", path, ports.ErrNotFound)
	}
	w.snapshots[ref] = path
	return "fake-snapshot", nil
}

// Push takes a ref, or refuses every one.
func (w *Worktrees) Push(_ context.Context, path, remote, ref string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.refuse || remote == "" {
		return ErrPushRefused
	}
	w.pushed = append(w.pushed, ref)
	if ref == lanes.HeadsRef(w.byPath[path].Branch) {
		w.ahead[path] = 0
	}
	return nil
}

// DefaultBranch names the branch every fake repository lands on.
func (w *Worktrees) DefaultBranch(context.Context, string) (string, error) { return "main", nil }

var _ ports.Worktrees = (*Worktrees)(nil)
