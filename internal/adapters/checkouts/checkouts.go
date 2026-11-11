// Package checkouts keeps a worker's own checkouts in a file of its state directory, and finds the git
// clones on its machine (ADR-0022).
package checkouts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
		dir, err := filepath.Abs(given)
		if err != nil {
			return nil, nil, err
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
// nothing, and a clone git cannot describe, such as one with no commit, says why.
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
	description, err := repofiles.New(repofiles.Config{Repository: real, Env: f.Env}).Describe(ctx)
	if err != nil {
		return ports.Clone{}, fmt.Sprintf("is a git clone git cannot describe: %v", err), false
	}
	return ports.Clone{
		Path: real, ID: description.Instance, Remote: description.Remote, DefaultBranch: description.DefaultBranch,
		Directory: filepath.Base(real),
	}, "", true
}

var _ ports.Checkouts = (*File)(nil)
