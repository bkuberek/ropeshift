package fake

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"
	"sync"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// CloneEdits is the edits of clones held in memory.
type CloneEdits struct {
	mu         sync.Mutex
	clones     map[string]ledger.CloneEdits
	failed     map[string]error
	diverged   map[string]ledger.Divergence
	unreadable map[string]error
	contents   map[string]string
	held       map[string]bool
	landed     []ledger.Landed
}

// NewCloneEdits returns a machine whose clones hold no edit.
func NewCloneEdits() *CloneEdits {
	return &CloneEdits{clones: map[string]ledger.CloneEdits{}, failed: map[string]error{}, diverged: map[string]ledger.Divergence{}, unreadable: map[string]error{}, contents: map[string]string{}, held: map[string]bool{}}
}

// Set places what the clone whose top is at a path holds.
func (c *CloneEdits) Set(clone string, edits ledger.CloneEdits) {
	c.mu.Lock()
	defer c.mu.Unlock()
	edits.Edits = slices.Clone(edits.Edits)
	slices.SortFunc(edits.Edits, func(a, b ledger.CloneEdit) int { return cmp.Compare(a.Path, b.Path) })
	c.clones[path.Clean(clone)] = edits
}

// Fail makes every read of a clone answer err.
func (c *CloneEdits) Fail(clone string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failed[path.Clean(clone)] = err
}

// Edits answers what was placed for the clone, cut at limit. A clone nothing was placed for holds no edit.
func (c *CloneEdits) Edits(_ context.Context, clone string, limit int) (ledger.CloneEdits, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	clone = path.Clean(clone)
	if err := c.failed[clone]; err != nil {
		return ledger.CloneEdits{}, fmt.Errorf("reading %s: %w", clone, err)
	}
	edits := c.clones[clone]
	edits.Edits = slices.Clone(edits.Edits)
	if edits.Edits == nil {
		edits.Edits = []ledger.CloneEdit{}
	}
	if limit >= 0 && len(edits.Edits) > limit {
		edits.Edits, edits.Cut = edits.Edits[:limit], true
	}
	return edits, nil
}

// Diverge places what a lane's branch in a clone and its base each changed.
func (c *CloneEdits) Diverge(clone, branch string, divergence ledger.Divergence) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.diverged[path.Clean(clone)+"\x00"+branch] = divergence
}

// FailDiverge makes every read of a lane's branch in a clone answer err.
func (c *CloneEdits) FailDiverge(clone, branch string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unreadable[path.Clean(clone)+"\x00"+branch] = err
}

// Undiverge takes away what was placed for a lane's branch, as a branch the clone no longer holds.
func (c *CloneEdits) Undiverge(clone, branch string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.diverged, path.Clean(clone)+"\x00"+branch)
}

// Landed lists the landed work each read of a lane's branch was given, in order.
func (c *CloneEdits) Landed() []ledger.Landed {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.landed)
}

// Diverged answers what was placed for the branch. A branch nothing was placed for is ErrNotFound.
func (c *CloneEdits) Diverged(_ context.Context, clone, branch, _ string, landed ledger.Landed, _ int) (ledger.Divergence, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.landed = append(c.landed, ledger.Landed{Commits: slices.Clone(landed.Commits), Ranges: slices.Clone(landed.Ranges)})
	if err := c.unreadable[path.Clean(clone)+"\x00"+branch]; err != nil {
		return ledger.Divergence{}, fmt.Errorf("reading %s in %s: %w", branch, clone, err)
	}
	divergence, ok := c.diverged[path.Clean(clone)+"\x00"+branch]
	if !ok {
		return ledger.Divergence{}, fmt.Errorf("%w: no branch %s in %s", ports.ErrNotFound, branch, clone)
	}
	divergence.Lane, divergence.Pushed = slices.Clone(divergence.Lane), slices.Clone(divergence.Pushed)
	return divergence, nil
}

// SetContents places the blob a path's file in a clone holds; empty is a file that is gone.
func (c *CloneEdits) SetContents(clone, file, blob string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.contents[path.Clean(clone)+"\x00"+file] = blob
}

// Contents answers what was placed for each path. A path nothing was placed for holds the blob named after it.
func (c *CloneEdits) Contents(_ context.Context, clone string, paths []string) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.failed[path.Clean(clone)]; err != nil {
		return nil, fmt.Errorf("reading %s: %w", clone, err)
	}
	out := make(map[string]string, len(paths))
	for _, file := range paths {
		blob, ok := c.contents[path.Clean(clone)+"\x00"+file]
		if !ok {
			blob = "blob-of-" + file
		}
		out[file] = blob
	}
	return out, nil
}

// Hold places that a lane's branch holds an ancestor branch.
func (c *CloneEdits) Hold(clone, branch, ancestor string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.held[path.Clean(clone)+"\x00"+branch+"\x00"+ancestor] = true
}

// Holds answers what Hold placed. A lane's branch no divergence was placed for is ErrNotFound.
func (c *CloneEdits) Holds(_ context.Context, clone, branch, ancestor string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.diverged[path.Clean(clone)+"\x00"+branch]; !ok {
		return false, fmt.Errorf("%w: no branch %s in %s", ports.ErrNotFound, branch, clone)
	}
	return c.held[path.Clean(clone)+"\x00"+branch+"\x00"+ancestor], nil
}

var _ ports.CloneEdits = (*CloneEdits)(nil)
