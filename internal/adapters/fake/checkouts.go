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
	// dangling are the symbolic links on the machine that lead to nothing.
	dangling map[string]bool
	// reserved are the directories the installation keeps for itself.
	reserved []string
	// spellings are the second spellings of a directory the machine holds, each to the one it spells.
	spellings map[string]string
	// ignored are the paths each clone's repository ignores, relative to the clone.
	ignored map[string][]string
}

// NewCheckouts returns a machine with no clone on it.
func NewCheckouts() *Checkouts {
	return &Checkouts{clones: map[string]ports.Clone{}, kept: map[ledger.WorkspaceID]map[string]string{}, links: map[string]string{}, spellings: map[string]string{}, ignored: map[string][]string{}}
}

// Link puts a symbolic link on the machine at a path, leading to an absolute path.
func (f *Checkouts) Link(at, target string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links[path.Clean(at)] = path.Clean(target)
}

// Dangle puts a symbolic link on the machine at a path that leads to a folder nothing is at. Resolve leaves the
// path as it is, as a disk gives no place for such a link, and DanglingLink names it.
func (f *Checkouts) Dangle(at string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dangling == nil {
		f.dangling = map[string]bool{}
	}
	f.dangling[path.Clean(at)] = true
}

// DanglingLink names the first component of target below a directory that is a link put there by Dangle.
func (f *Checkouts) DanglingLink(_ context.Context, below, target string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	below = path.Clean(below)
	rest, found := strings.CutPrefix(path.Clean(target), below+"/")
	if !found {
		return "", nil
	}
	at := below
	for part := range strings.SplitSeq(rest, "/") {
		at = path.Join(at, part)
		if f.dangling[at] {
			return at, nil
		}
	}
	return "", nil
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
func (f *Checkouts) SameFile(_ context.Context, a, b string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	first, _ := f.resolveLocked(a)
	second, _ := f.resolveLocked(b)
	// As on a disk, a path nothing is at is ErrNotFound, whatever the other path is.
	for _, at := range []string{first, second} {
		if !f.existsLocked(at) {
			return false, fmt.Errorf("nothing is at %s: %w", at, ports.ErrNotFound)
		}
	}
	return f.spelled(first) == f.spelled(second), nil
}

// existsLocked says the machine holds a directory at a path: a clone, or a directory above one or inside one,
// a path the machine reads another path as, or a checkout this worker keeps. The machine holds no other
// directory. The caller holds the lock.
func (f *Checkouts) existsLocked(at string) bool {
	at = f.spelled(at)
	holds := func(held string) bool {
		held = f.spelled(held)
		return at == held || strings.HasPrefix(at, held+"/") || strings.HasPrefix(held, at+"/")
	}
	if slices.ContainsFunc(slices.Collect(maps.Keys(f.clones)), holds) || slices.ContainsFunc(slices.Collect(maps.Values(f.spellings)), holds) ||
		slices.ContainsFunc(f.reserved, holds) {
		return true
	}
	for _, held := range f.kept {
		if slices.ContainsFunc(slices.Collect(maps.Values(held)), holds) {
			return true
		}
	}
	return false
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

// Ignore says the repository whose clone is at a path ignores each of the paths given, relative to the clone, and
// everything inside it, as a .gitignore line for a folder does.
func (f *Checkouts) Ignore(clone string, paths ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	clone = path.Clean(clone)
	for _, ignored := range paths {
		f.ignored[clone] = append(f.ignored[clone], path.Clean(ignored))
	}
}

// Ignored says a clone's repository ignores a path, relative to the clone, which it does when the path or a folder
// above it was named to Ignore. A clone that ignores nothing ignores nothing.
func (f *Checkouts) Ignored(_ context.Context, clone, at string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	at = path.Clean(at)
	return slices.ContainsFunc(f.ignored[path.Clean(clone)], func(held string) bool { return at == held || strings.HasPrefix(at, held+"/") }), nil
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
	real, reserved := f.resolveLocked(at)
	return real, reserved, nil
}

// resolveLocked follows the links in a path. The caller holds the lock.
func (f *Checkouts) resolveLocked(at string) (string, bool) {
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
	reserved := ports.InRopeshiftFolder(at) || slices.ContainsFunc(f.reserved, func(dir string) bool { return at == dir || strings.HasPrefix(at, dir+"/") })
	return at, reserved
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
		// A path that leads through a symbolic link is the directory it leads to, as on a disk.
		given, _ = f.resolveLocked(given)
		if ports.InRopeshiftFolder(given) {
			unread = append(unread, ports.UnreadPath{Path: given, Reason: "is inside a .ropeshift folder, which holds lane worktrees and no clone to register"})
			continue
		}
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

// KeepIfAbsent keeps the checkout only while none is kept.
func (f *Checkouts) KeepIfAbsent(_ context.Context, project ledger.WorkspaceID, repository, at string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, kept := f.kept[project][repository]; kept {
		return false, nil
	}
	if f.kept[project] == nil {
		f.kept[project] = map[string]string{}
	}
	f.kept[project][repository] = at
	return true, nil
}

func (f *Checkouts) Forget(_ context.Context, project ledger.WorkspaceID, repository string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.kept[project], repository)
	return nil
}

// ForgetAt drops the checkout only while it is kept at path.
func (f *Checkouts) ForgetAt(_ context.Context, project ledger.WorkspaceID, repository, at string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if kept, ok := f.kept[project][repository]; !ok || kept != at {
		return false, nil
	}
	delete(f.kept[project], repository)
	return true, nil
}

func (f *Checkouts) Of(_ context.Context, project ledger.WorkspaceID) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.kept[project]), nil
}

// Every lists the checkout of every repository in every project, each path once and in order.
func (f *Checkouts) Every(_ context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var every []string
	for _, held := range f.kept {
		every = append(every, slices.Collect(maps.Values(held))...)
	}
	slices.Sort(every)
	return slices.Compact(every), nil
}

// All lists every checkout with its project and repository, by project and then repository.
func (f *Checkouts) All(_ context.Context) ([]ports.Checkout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []ports.Checkout
	for _, project := range slices.Sorted(maps.Keys(f.kept)) {
		held := f.kept[project]
		for _, repository := range slices.Sorted(maps.Keys(held)) {
			all = append(all, ports.Checkout{Project: project, Repository: repository, Path: held[repository]})
		}
	}
	return all, nil
}

var _ ports.Checkouts = (*Checkouts)(nil)
var _ ports.KeepsIfAbsent = (*Checkouts)(nil)
