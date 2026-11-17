// Package projectfolders is a worker's access to the folder a template write fills: it reads what is at a
// path, writes a shape's files into a folder, and makes the folder's first commit with git (FR-TPL-5).
package projectfolders

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
)

// Folders reads and writes project folders on this machine.
type Folders struct {
	// Env replaces the environment of git commands. Nil runs them in the process's.
	Env []string
}

// Inspect reads what is at an absolute path: whether it exists, is a link, is an empty directory, whether its
// parent is a directory, where it really is, and the top of the clone that holds it.
func (f Folders) Inspect(ctx context.Context, path string) (ports.FolderInspection, error) {
	var found ports.FolderInspection
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		found.Exists = true
		found.Link = info.Mode()&os.ModeSymlink != 0
		found.Directory = info.IsDir()
	case absent(err):
	default:
		return found, err
	}
	if parent, err := os.Stat(filepath.Dir(path)); err == nil && parent.IsDir() {
		found.ParentDirectory = true
	}
	if found.Directory {
		entries, err := os.ReadDir(path)
		if err != nil {
			return found, err
		}
		found.Empty = len(entries) == 0
	}
	if found.Real, err = realPath(path); err != nil {
		return found, err
	}
	found.CloneTop = f.cloneTop(ctx, path, found)
	return found, nil
}

// absent says an error is the path, or a part of it, not being there.
func absent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// realPath follows every link in the part of a path that exists and keeps the rest as it is spelled.
func realPath(path string) (string, error) {
	at, rest := filepath.Clean(path), ""
	for {
		real, err := filepath.EvalSymlinks(at)
		if err == nil {
			return filepath.Join(real, rest), nil
		}
		parent := filepath.Dir(at)
		if !absent(err) || parent == at {
			return "", err
		}
		rest, at = filepath.Join(filepath.Base(at), rest), parent
	}
}

// cloneTop asks git for the top of the working tree that holds a directory: the folder itself when it is one,
// and the folder's parent when it is absent. Git failing, or not being there, says no clone holds it.
func (f Folders) cloneTop(ctx context.Context, path string, found ports.FolderInspection) string {
	dir := filepath.Dir(path)
	if found.Directory && !found.Link {
		dir = path
	} else if !found.ParentDirectory {
		return ""
	}
	top, err := gitcli.Git{Dir: dir, Env: f.Env}.Text(ctx, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	return top
}

// Write makes the folder when it is absent, never its parents, and each file under it with the folders the file
// needs. A file opens only to be created, so one that exists stays as it is.
func (f Folders) Write(_ context.Context, path string, files []ports.FolderFile) error {
	parent, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	name := filepath.Base(path)
	if err := parent.Mkdir(name, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	// A folder that became a link between the inspection and now is not written through.
	info, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is no longer a folder", path)
	}
	// Every folder and file is made through the folder's root, which refuses a link that leads out of it, so a link
	// planted at a nested folder, or swapped in after the checks, is not followed.
	folder, err := parent.OpenRoot(name)
	if err != nil {
		return err
	}
	defer func() { _ = folder.Close() }()
	for _, file := range files {
		local := filepath.FromSlash(file.Path)
		if !filepath.IsLocal(local) {
			return fmt.Errorf("%s names a path outside the folder", file.Path)
		}
		if dir := filepath.Dir(local); dir != "." {
			if err := folder.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		out, err := folder.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		_, err = out.WriteString(file.Text)
		if closed := out.Close(); err == nil {
			err = closed
		}
		if err != nil {
			return fmt.Errorf("write %s: %w", file.Path, err)
		}
	}
	return nil
}

// Files lists every file under the folder, leaving out the git directory at its top.
func (f Folders) Files(_ context.Context, path string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(path, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, at)
		if err != nil || rel == "." {
			return err
		}
		if rel == ".git" {
			return fs.SkipDir
		}
		if !entry.IsDir() {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if absent(err) {
		return nil, nil
	}
	slices.Sort(out)
	return out, err
}

// FirstCommit makes the folder a repository, adds its files, and commits them. It runs under no hook and no signing,
// whatever the user's git configuration names, and `git init` copies no template, because the folder's hooks are code
// the commit would run. A .git is the worker's own only when the caller says its journal records the git init: then a
// commit made before a crash is answered, not made twice, and an init with no commit is gone on from.
func (f Folders) FirstCommit(ctx context.Context, path, message string, resumed bool) (string, error) {
	git := gitcli.Git{Dir: path, Env: f.Env, Extra: gitcli.NoHooks()}
	if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
		if !resumed {
			return "", errors.New("the folder holds a repository this worker did not make")
		}
		if id, err := git.Text(ctx, "rev-parse", "--verify", "HEAD"); err == nil {
			return id, nil
		}
	}
	for _, args := range [][]string{{"init", "--quiet", "--template="}, {"add", "-A"}, {"-c", "commit.gpgsign=false", "commit", "--quiet", "-m", message}} {
		if _, err := git.Output(ctx, args...); err != nil {
			return "", describe(err)
		}
	}
	return git.Text(ctx, "rev-parse", "--verify", "HEAD")
}

// describe keeps what git said, which is what a person needs to fix the commit, and drops the command line and
// the exit status that gitcli puts before it.
func describe(err error) error {
	text := err.Error()
	if _, after, found := strings.Cut(text, ": exit status "); found {
		if _, said, found := strings.Cut(after, ": "); found && strings.TrimSpace(said) != "" {
			return errors.New(strings.TrimSpace(said))
		}
	}
	return err
}

var _ ports.ProjectFolders = Folders{}
