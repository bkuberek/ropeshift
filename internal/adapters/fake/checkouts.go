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
	// links are the symbolic links on the machine, each path to the path it leads to.
	links map[string]string
	// reserved are the directories the installation keeps for itself.
	reserved []string
	// spellings are the second spellings of a directory the machine holds, each to the one it spells.
	spellings map[string]string
}

// NewCheckouts returns a machine with no clone on it.
func NewCheckouts() *Checkouts {
	return &Checkouts{clones: map[string]ports.Clone{}, kept: map[ledger.WorkspaceID]map[string]string{}, links: map[string]string{}, spellings: map[string]string{}}
}

// Link puts a symbolic link on the machine at a path, leading to an absolute path.
func (f *Checkouts) Link(at, target string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links[path.Clean(at)] = path.Clean(target)
}

// Spell says a path is a second spelling of another, as a disk that ignores case gives a directory, so the two
// name one file. Resolve leaves a spelling as it is, as a machine does, and SameFile says they match.
func (f *Checkouts) Spell(variant, of string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spellings[path.Clean(variant)] = path.Clean(of)
}

// SameFile says two paths name one file once the links in each are followed and the spellings the machine
// holds are read as the paths they spell.
func (f *Checkouts) SameFile(ctx context.Context, a, b string) (bool, error) {
	first, _, err := f.Resolve(ctx, a)
	if err != nil {
		return false, err
	}
	second, _, err := f.Resolve(ctx, b)
	if err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spelled(first) == f.spelled(second), nil
}

// spelled reads a path as the path it spells, a spelling of one of its directories included.
func (f *Checkouts) spelled(at string) string {
	for variant, of := range f.spellings {
		if at == variant {
			return of
		}
		if rest, under := strings.CutPrefix(at, variant+"/"); under {
			return of + "/" + rest
		}
	}
	return at
}

// Reserve names a directory the installation keeps for itself, as its state directory.
func (f *Checkouts) Reserve(dir string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reserved = append(f.reserved, path.Clean(dir))
}

// Resolve follows the links in a path, the first component that is a link first, as a machine does. Every
// path is there, because the machine holds no directories, only clones and links.
func (f *Checkouts) Resolve(_ context.Context, at string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	at = path.Clean(at)
	for range 40 {
		moved := false
		parts := strings.Split(at, "/")
		for i := 1; i <= len(parts) && !moved; i++ {
			prefix := strings.Join(parts[:i], "/")
			if target, ok := f.links[prefix]; ok {
				at, moved = path.Join(append([]string{target}, parts[i:]...)...), true
			}
		}
		if !moved {
			break
		}
	}
	reserved := slices.ContainsFunc(f.reserved, func(dir string) bool { return at == dir || strings.HasPrefix(at, dir+"/") })
	return at, reserved, nil
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
