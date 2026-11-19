// Package checkouts keeps a worker's own checkouts in a file of its state directory, and finds the git
// clones on its machine (ADR-0022).
package checkouts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// FileName is the file in a worker's state directory that holds its checkouts.
const FileName = "checkouts.json"

// File is a worker's checkouts, by project and repository, in one file only this worker writes. A write
// replaces the file whole, so a reader sees the last checkouts or the ones before, never a part.
type File struct {
	// Path is the file. It need not exist yet.
	Path string
	// Env replaces the environment of git commands. Nil runs them in the process's.
	Env []string
	// Reserved are the directories the installation keeps for itself, such as its state and run
	// directories, which hold no clone to register.
	Reserved []string

	mu sync.Mutex
}

// document is the file's content: the path of each repository's checkout, by project.
type document struct {
	Projects map[ledger.WorkspaceID]map[string]string `json:"projects"`
}

func (f *File) read() (document, error) {
	doc := document{Projects: map[ledger.WorkspaceID]map[string]string{}}
	raw, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return doc, err
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, fmt.Errorf("%s: %w", f.Path, err)
	}
	if doc.Projects == nil {
		doc.Projects = map[ledger.WorkspaceID]map[string]string{}
	}
	return doc, nil
}

// write replaces the file through a rename, so a crash leaves the old file or the new one.
func (f *File) write(doc document) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(f.Path), "."+filepath.Base(f.Path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err := temp.Write(append(raw, '\n')); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), f.Path)
}

// Keep records path as this worker's checkout of a project's repository.
func (f *File) Keep(_ context.Context, project ledger.WorkspaceID, repository, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.read()
	if err != nil {
		return err
	}
	if doc.Projects[project] == nil {
		doc.Projects[project] = map[string]string{}
	}
	doc.Projects[project][repository] = path
	return f.write(doc)
}

// KeepIfAbsent records a checkout only while the worker keeps none of the repository, under the same lock as Keep,
// so a checkout another caller kept is never replaced.
func (f *File) KeepIfAbsent(_ context.Context, project ledger.WorkspaceID, repository, path string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.read()
	if err != nil {
		return false, err
	}
	if _, kept := doc.Projects[project][repository]; kept {
		return false, nil
	}
	if doc.Projects[project] == nil {
		doc.Projects[project] = map[string]string{}
	}
	doc.Projects[project][repository] = path
	return true, f.write(doc)
}

// Forget drops this worker's checkout of a project's repository. Forgetting one it never kept writes
// nothing.
func (f *File) Forget(_ context.Context, project ledger.WorkspaceID, repository string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.read()
	if err != nil {
		return err
	}
	if _, kept := doc.Projects[project][repository]; !kept {
		return nil
	}
	delete(doc.Projects[project], repository)
	if len(doc.Projects[project]) == 0 {
		delete(doc.Projects, project)
	}
	return f.write(doc)
}

// ForgetAt drops this worker's checkout of a project's repository only while the file keeps it at path, read and
// written under one lock, so a checkout kept again at another path stays.
func (f *File) ForgetAt(_ context.Context, project ledger.WorkspaceID, repository, path string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.read()
	if err != nil {
		return false, err
	}
	if kept, ok := doc.Projects[project][repository]; !ok || kept != path {
		return false, nil
	}
	delete(doc.Projects[project], repository)
	if len(doc.Projects[project]) == 0 {
		delete(doc.Projects, project)
	}
	return true, f.write(doc)
}

// Of lists this worker's checkouts in a project.
func (f *File) Of(_ context.Context, project ledger.WorkspaceID) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.read()
	if err != nil {
		return nil, err
	}
	return maps.Clone(doc.Projects[project]), nil
}

// Every lists the path of every checkout this worker keeps, in every project, each once and in order.
func (f *File) Every(_ context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.read()
	if err != nil {
		return nil, err
	}
	var every []string
	for _, held := range doc.Projects {
		every = append(every, slices.Collect(maps.Values(held))...)
	}
	slices.Sort(every)
	return slices.Compact(every), nil
}

// All lists every checkout this worker keeps with its project and repository, by project and then repository.
func (f *File) All(_ context.Context) ([]ports.Checkout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.read()
	if err != nil {
		return nil, err
	}
	var all []ports.Checkout
	for _, project := range slices.Sorted(maps.Keys(doc.Projects)) {
		held := doc.Projects[project]
		for _, repository := range slices.Sorted(maps.Keys(held)) {
			all = append(all, ports.Checkout{Project: project, Repository: repository, Path: held[repository]})
		}
	}
	return all, nil
}

// Find lists the clones at paths. A path that is the top of a clone is that clone, and any other
// directory holds the clones among its children down to depth levels. A clone's own directories are not
// searched, and neither are hidden ones.
func (f *File) Find(ctx context.Context, paths []string, depth int) ([]ports.Clone, []ports.UnreadPath, error) {
	var found []ports.Clone
	var unread []ports.UnreadPath
	seen := map[string]bool{}
	add := func(clones []ports.Clone) {
		for _, clone := range clones {
			if !seen[clone.Path] {
				seen[clone.Path] = true
				found = append(found, clone)
			}
		}
	}
	for _, given := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		dir, err := filepath.Abs(given)
		if err != nil {
			return nil, nil, err
		}
		if ports.InRopeshiftFolder(dir) {
			unread = append(unread, ports.UnreadPath{Path: dir, Reason: "is inside a .ropeshift folder, which holds lane worktrees and no clone to register"})
			continue
		}
		info, err := os.Stat(dir)
		switch {
		case errors.Is(err, os.ErrNotExist):
			unread = append(unread, ports.UnreadPath{Path: dir, Reason: "does not exist"})
			continue
		case err != nil:
			unread = append(unread, ports.UnreadPath{Path: dir, Reason: err.Error()})
			continue
		case !info.IsDir():
			unread = append(unread, ports.UnreadPath{Path: dir, Reason: "is not a directory"})
			continue
		}
		if clone, reason, ok := f.cloneAt(ctx, dir); ok || reason != "" {
			if ok {
				add([]ports.Clone{clone})
			} else {
				unread = append(unread, ports.UnreadPath{Path: dir, Reason: reason})
			}
			continue
		}
		clones, refused := f.below(ctx, dir, depth)
		unread = append(unread, refused...)
		if len(clones) == 0 && len(refused) == 0 {
			unread = append(unread, ports.UnreadPath{Path: dir, Reason: fmt.Sprintf("is not a git clone and holds none %s down", levels(depth))})
		}
		add(clones)
	}
	// A walk that stopped for its context found only part of the tree.
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return found, unread, nil
}

func levels(depth int) string {
	if depth == 1 {
		return "1 level"
	}
	return fmt.Sprintf("%d levels", depth)
}

// below finds the clones among a directory's children, down to depth levels, and the clones git cannot
// describe, which are refused with why.
func (f *File) below(ctx context.Context, dir string, depth int) ([]ports.Clone, []ports.UnreadPath) {
	entries, err := os.ReadDir(dir)
	if err != nil || depth < 1 {
		return nil, nil
	}
	var found []ports.Clone
	var unread []ports.UnreadPath
	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		child := filepath.Join(dir, entry.Name())
		clone, reason, ok := f.cloneAt(ctx, child)
		switch {
		case ok:
			found = append(found, clone)
		case reason != "":
			unread = append(unread, ports.UnreadPath{Path: child, Reason: reason})
		default:
			deeper, refused := f.below(ctx, child, depth-1)
			found, unread = append(found, deeper...), append(unread, refused...)
		}
	}
	return found, unread
}

// cloneAt reads the clone whose working tree's top is dir. A directory that is no clone's top says
// nothing, and a clone git cannot describe, such as one with no commit, says why, as a shallow clone does.
func (f *File) cloneAt(ctx context.Context, dir string) (ports.Clone, string, bool) {
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		return ports.Clone{}, "", false
	}
	top, err := gitcli.Git{Dir: dir, Env: f.Env}.Text(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return ports.Clone{}, "", false
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ports.Clone{}, "", false
	}
	if resolved, err := filepath.EvalSymlinks(top); err != nil || resolved != real {
		return ports.Clone{}, "", false
	}
	source := repofiles.New(repofiles.Config{Repository: real, Env: f.Env})
	description, err := source.Describe(ctx)
	var shallow *repofiles.ShallowCloneError
	switch {
	case errors.As(err, &shallow):
		return ports.Clone{}, ledger.ShallowClone, false
	case err != nil:
		return ports.Clone{}, fmt.Sprintf("is a git clone git cannot describe: %v", err), false
	}
	roots, err := source.Roots(ctx)
	if err != nil {
		return ports.Clone{}, fmt.Sprintf("is a git clone git cannot describe: %v", err), false
	}
	return ports.Clone{
		Path: real, Entered: dir, ID: description.Instance, Roots: roots, Remote: description.Remote, DefaultBranch: description.DefaultBranch,
		Directory: filepath.Base(real),
	}, "", true
}

// Resolve follows every symbolic link in a path, and says whether the path it comes to is inside a
// directory the installation keeps for itself, or inside a .ropeshift folder, where a workspace keeps lane
// worktrees. A path nothing is at is ErrNotFound.
func (f *File) Resolve(_ context.Context, path string) (string, bool, error) {
	real, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return "", false, fmt.Errorf("nothing is at %s: %w", path, ports.ErrNotFound)
	}
	if err != nil {
		return "", false, err
	}
	if real, err = filepath.Abs(real); err != nil {
		return "", false, err
	}
	// A workspace keeps its lane worktrees under its .ropeshift folder, which no registration takes for a clone.
	if ports.InRopeshiftFolder(real) {
		return real, true, nil
	}
	for _, dir := range f.Reserved {
		if held, err := filepath.EvalSymlinks(dir); err == nil {
			dir = held
		}
		if rel, err := filepath.Rel(filepath.Clean(dir), real); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return real, true, nil
		}
	}
	// A directory spelled another way is the same directory, as on a disk that ignores case, so the path and
	// each directory above it are compared with the reserved ones by the file each names.
	inside, err := insideByFile(f.Reserved, real)
	return real, inside, err
}

// insideByFile says a path is one of the directories, or inside one, by the file each names and not by how a
// path spells it: the path and every directory above it are read from the disk and compared with each
// directory that exists. A directory nothing is at holds nothing.
func insideByFile(dirs []string, path string) (bool, error) {
	var reserved []os.FileInfo
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err == nil {
			reserved = append(reserved, info)
		}
	}
	if len(reserved) == 0 {
		return false, nil
	}
	for at := filepath.Clean(path); ; at = filepath.Dir(at) {
		info, err := os.Stat(at)
		if err != nil {
			return false, err
		}
		if slices.ContainsFunc(reserved, func(dir os.FileInfo) bool { return os.SameFile(dir, info) }) {
			return true, nil
		}
		if filepath.Dir(at) == at {
			return false, nil
		}
	}
}

// DanglingLink reads each component of target below the directory below with Lstat, and names the first that is
// a symbolic link whose target does not resolve, whether it is missing or a link further along leads nowhere. A
// component nothing is at ends the walk. A target that does not lie below the directory has nothing to walk.
func (f *File) DanglingLink(_ context.Context, below, target string) (string, error) {
	rest, found := strings.CutPrefix(filepath.Clean(target), filepath.Clean(below)+string(filepath.Separator))
	if !found {
		return "", nil
	}
	at := filepath.Clean(below)
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		at = filepath.Join(at, part)
		info, err := os.Lstat(at)
		switch {
		case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
			return "", nil
		case err != nil:
			return "", err
		case info.Mode()&os.ModeSymlink == 0:
			continue
		}
		if _, err := filepath.EvalSymlinks(at); errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return at, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", nil
}

// SameFile says two paths name one file or directory, by what the disk says of each. A path nothing is at is
// ErrNotFound.
func (f *File) SameFile(_ context.Context, a, b string) (bool, error) {
	first, err := statOf(a)
	if err != nil {
		return false, err
	}
	second, err := statOf(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(first, second), nil
}

// Ignored says the repository whose clone is at clone ignores path, which `git check-ignore` answers: exit 0
// for a path it ignores, 1 for one it does not, and any other status is git failing to answer.
func (f *File) Ignored(ctx context.Context, clone, path string) (bool, error) {
	_, err := gitcli.Git{Dir: clone, Env: f.Env}.Output(ctx, "check-ignore", "-q", "--", path)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, err
}

// statOf reads what the disk says of a path, every symbolic link followed.
func statOf(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, fmt.Errorf("nothing is at %s: %w", path, ports.ErrNotFound)
	}
	return info, err
}

var _ ports.Checkouts = (*File)(nil)
