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
	mu     sync.Mutex
	clones map[string]ledger.CloneEdits
	failed map[string]error
}

// NewCloneEdits returns a machine whose clones hold no edit.
func NewCloneEdits() *CloneEdits {
	return &CloneEdits{clones: map[string]ledger.CloneEdits{}, failed: map[string]error{}}
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

var _ ports.CloneEdits = (*CloneEdits)(nil)
