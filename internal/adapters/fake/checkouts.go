package fake

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Checkouts is a worker's machine held in memory: the clones a test places on it, and the checkouts the
// worker keeps. A path is a slash-separated name, and nothing touches a disk.
type Checkouts struct {
	mu     sync.Mutex
	clones map[string]ports.Clone
	kept   map[ledger.WorkspaceID]map[string]string
}

// NewCheckouts returns a machine with no clone on it.
func NewCheckouts() *Checkouts {
	return &Checkouts{clones: map[string]ports.Clone{}, kept: map[ledger.WorkspaceID]map[string]string{}}
}

// Place puts a clone on the machine at its path, and names its directory after the path when it names none.
func (f *Checkouts) Place(clone ports.Clone) {
	f.mu.Lock()
	defer f.mu.Unlock()
	clone.Path = path.Clean(clone.Path)
	if clone.Directory == "" {
		clone.Directory = path.Base(clone.Path)
	}
	f.clones[clone.Path] = clone
}

// Remove takes a clone off the machine, as a person who deletes or moves it does.
func (f *Checkouts) Remove(at string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.clones, path.Clean(at))
}

// Find lists the clones at each path, or below it to depth levels, as the worker's own machine would.
func (f *Checkouts) Find(_ context.Context, paths []string, depth int) ([]ports.Clone, []ports.UnreadPath, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []ports.Clone
	var unread []ports.UnreadPath
	for _, given := range paths {
		given = path.Clean(given)
		if clone, ok := f.clones[given]; ok {
			found = append(found, clone)
			continue
		}
		var below []ports.Clone
		for _, at := range slices.Sorted(maps.Keys(f.clones)) {
			rest, under := strings.CutPrefix(at, given+"/")
			if under && strings.Count(rest, "/") < depth {
				below = append(below, f.clones[at])
			}
		}
		if len(below) == 0 {
			unread = append(unread, ports.UnreadPath{Path: given, Reason: fmt.Sprintf("is not a git clone and holds none %d levels down", depth)})
		}
		found = append(found, below...)
	}
	return found, unread, nil
}

func (f *Checkouts) Keep(_ context.Context, project ledger.WorkspaceID, repository, at string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.kept[project] == nil {
		f.kept[project] = map[string]string{}
	}
	f.kept[project][repository] = at
	return nil
}

func (f *Checkouts) Forget(_ context.Context, project ledger.WorkspaceID, repository string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.kept[project], repository)
	return nil
}

func (f *Checkouts) Of(_ context.Context, project ledger.WorkspaceID) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.kept[project]), nil
}

var _ ports.Checkouts = (*Checkouts)(nil)
