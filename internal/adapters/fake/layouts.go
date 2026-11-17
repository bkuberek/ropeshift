package fake

import (
	"context"
	"fmt"
	"path"
	"slices"
	"sync"

	"ropeshift.com/ropeshift/internal/ports"
)

// CloneLayout is what a test places for one clone: the directories it ignores, the plan content at its tip, and
// the paths its tip tracks. Tip names the commit, and empty gives one made from the clone's path.
type CloneLayout struct {
	Ignored []string
	Plan    ports.PlanContent
	Tracked []string
	Tip     string
}

// Layouts is the layout of clones held in memory.
type Layouts struct {
	mu     sync.Mutex
	clones map[string]CloneLayout
}

// NewLayouts returns a machine with no clone laid out.
func NewLayouts() *Layouts { return &Layouts{clones: map[string]CloneLayout{}} }

// Set places the layout of the clone whose top is at a path.
func (l *Layouts) Set(clone string, layout CloneLayout) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if layout.Tip == "" {
		layout.Tip = "tip-of-" + path.Clean(clone)
	}
	layout.Tracked = slices.Sorted(slices.Values(layout.Tracked))
	layout.Ignored = slices.Sorted(slices.Values(layout.Ignored))
	l.clones[path.Clean(clone)] = layout
}

// read is the layout placed for a clone. A clone none was placed for holds nothing: it ignores no directory,
// holds no plan content, and tracks no path.
func (l *Layouts) read(clone string) (CloneLayout, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	clone = path.Clean(clone)
	layout, ok := l.clones[clone]
	if !ok {
		return CloneLayout{Tip: "tip-of-" + clone}, nil
	}
	return layout, nil
}

// Ignored lists the ignored directories placed for the clone.
func (l *Layouts) Ignored(_ context.Context, clone string) ([]string, error) {
	layout, err := l.read(clone)
	return slices.Clone(layout.Ignored), err
}

// Plan reads the plan content placed for the clone.
func (l *Layouts) Plan(_ context.Context, clone string) (ports.PlanContent, error) {
	layout, err := l.read(clone)
	return layout.Plan, err
}

// Documents counts the Markdown paths the clone's tip tracks.
func (l *Layouts) Documents(_ context.Context, clone string) (int, error) {
	layout, err := l.read(clone)
	n := 0
	for _, tracked := range layout.Tracked {
		if path.Ext(tracked) == ".md" {
			n++
		}
	}
	return n, err
}

// Tip is the commit placed for the clone.
func (l *Layouts) Tip(_ context.Context, clone string) (string, error) {
	layout, err := l.read(clone)
	return layout.Tip, err
}

// Tracked lists the paths the clone's tip tracks. The clone holds no other commit.
func (l *Layouts) Tracked(_ context.Context, clone, commit string) ([]string, error) {
	layout, err := l.read(clone)
	if err != nil {
		return nil, err
	}
	if commit != layout.Tip {
		return nil, fmt.Errorf("%s holds no commit %s: %w", clone, commit, ports.ErrNotFound)
	}
	return slices.Clone(layout.Tracked), nil
}

// CodeTrees reads the code trees of the paths the clone's tip tracks.
func (l *Layouts) CodeTrees(_ context.Context, clone string) ([]ports.CodeTree, error) {
	layout, err := l.read(clone)
	return ports.CodeTreesOf(layout.Tracked), err
}

// Instructions says the clone's tip tracks an instruction file at its top.
func (l *Layouts) Instructions(_ context.Context, clone string) (bool, error) {
	layout, err := l.read(clone)
	return ports.HoldsInstructions(layout.Tracked), err
}

var _ ports.Layouts = (*Layouts)(nil)
