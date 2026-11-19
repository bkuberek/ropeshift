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
	"strings"
	"syscall"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
)

// Folders reads and writes project folders on this machine.
type Folders struct {
	// Env replaces the environment of git commands. Nil runs them in the process's.
	Env []string
	// between, when set, runs between the check that the folder is a folder and the opening of it, so a test can swap
	// the folder at the one moment no real swap is timed to land.
	between func()
}

// folderID names the folder a file's info is of by its device and inode, which a link swapped in for it does not share.
func folderID(info os.FileInfo) ports.FolderID {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return ports.FolderID(fmt.Sprintf("%d:%d", uint64(stat.Dev), uint64(stat.Ino))) //nolint:unconvert // the width of Dev differs by platform
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
func (f Folders) Write(_ context.Context, path string, files []ports.FolderFile, want ports.FolderID) (ports.FolderID, error) {
	parent, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	defer func() { _ = parent.Close() }()
	name := filepath.Base(path)
	if err := parent.Mkdir(name, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	// A folder that became a link between the inspection and now is not written through.
	checked, err := parent.Lstat(name)
	if err != nil {
		return "", err
	}
	if !checked.IsDir() {
		return "", fmt.Errorf("%s is no longer a folder", path)
	}
	if f.between != nil {
		f.between()
	}
	// Every folder and file is made through the folder's root, which refuses a link that leads out of it, so a link
	// planted at a nested folder is not followed.
	folder, err := parent.OpenRoot(name)
	if err != nil {
		// The check found a folder here, so a root that cannot be opened on it is a link that leads out, swapped in since.
		return "", fmt.Errorf("%w: %v", ports.ErrFolderChanged, err)
	}
	defer func() { _ = folder.Close() }()
	// The check ran on the path, and the root is what the open handle holds, so the handle is compared with what the
	// check found: a folder swapped in after the check is another file, and nothing is written into it.
	opened, err := folder.Stat(".")
	if err != nil {
		return "", err
	}
	id := folderID(opened)
	if !os.SameFile(checked, opened) || id == "" || (want != "" && id != want) {
		return "", ports.ErrFolderChanged
	}
	for _, file := range files {
		local := filepath.FromSlash(file.Path)
		if !filepath.IsLocal(local) {
			return "", fmt.Errorf("%s names a path outside the folder", file.Path)
		}
		if dir := filepath.Dir(local); dir != "." {
			if err := folder.MkdirAll(dir, 0o755); err != nil {
				return "", err
			}
		}
		out, err := folder.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = out.WriteString(file.Text)
		if closed := out.Close(); err == nil {
			err = closed
		}
		if err != nil {
			return "", fmt.Errorf("write %s: %w", file.Path, err)
		}
	}
	return id, nil
}

// Walk visits each file under the folder and each .git at any depth, in the order WalkDir reads them, and stops when
// visit answers false.
func (f Folders) Walk(_ context.Context, path string, visit func(path string, git bool) bool) error {
	err := filepath.WalkDir(path, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, at)
		if err != nil || rel == "." {
			return err
		}
		git := entry.Name() == ".git"
		switch {
		case git:
			if !visit(filepath.ToSlash(rel), true) {
				return fs.SkipAll
			}
			if entry.IsDir() {
				return fs.SkipDir
			}
		case !entry.IsDir():
			if !visit(filepath.ToSlash(rel), false) {
				return fs.SkipAll
			}
		}
		return nil
	})
	if absent(err) {
		return nil
	}
	return err
}

// Held says which of the paths the folder holds, looking at each with lstat.
func (f Folders) Held(_ context.Context, path string, files []string) ([]string, error) {
	var held []string
	for _, file := range files {
		local := filepath.FromSlash(file)
		if !filepath.IsLocal(local) {
			return nil, fmt.Errorf("%s names a path outside the folder", file)
		}
		// Anything but a path that is absent counts as held, so a file in a folder that cannot be read is not added.
		if _, err := os.Lstat(filepath.Join(path, local)); !errors.Is(err, fs.ErrNotExist) {
			held = append(held, file)
		}
	}
	return held, nil
}

// FirstCommit makes the folder a repository on the default branch, adds the files the commit names, and commits them. It runs under no hook and no signing,
// whatever the user's git configuration names, and `git init` copies no template, because the folder's hooks are code
// the commit would run. A .git is the worker's own only when the caller says its journal records the git init: then a
// commit made before a crash is answered, not made twice, and an init with no commit is gone on from.
func (f Folders) FirstCommit(ctx context.Context, path, message string, resumed bool, folder ports.FolderID, commit ports.CommitSpec) (string, error) {
	git := gitcli.Git{Dir: path, Env: f.Env, Config: gitcli.NoHooksNoSigning()}
	// Git finds the folder by its path, so the path is read again just before each command, and a path that is no longer
	// the folder Write named runs nothing.
	same := func() error {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || folder == "" || folderID(info) != folder {
			return ports.ErrFolderChanged
		}
		return nil
	}
	if err := same(); err != nil {
		return "", err
	}
	if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
		if !resumed {
			return "", errors.New("the folder holds a repository this worker did not make")
		}
		if id, err := git.Text(ctx, "rev-parse", "--verify", "HEAD"); err == nil {
			return id, nil
		}
	}
	if len(commit.Paths) == 0 {
		return "", errNothingToCommit
	}
	// The paths are files, so a path that starts with a dash or holds a glob names only the file it names.
	git.Extra = append(git.Extra, "GIT_LITERAL_PATHSPECS=1")
	if err := same(); err != nil {
		return "", err
	}
	if _, err := git.Output(ctx, "init", "--quiet", "--template=", "--initial-branch="+f.DefaultBranch(ctx, path)); err != nil {
		return "", describe(err)
	}
	// Git refuses a path it ignores, so the commit adds the paths git would add, as `git add -A` did: those it tracks, which
	// a run that died after its add already holds in the index, and those it does not ignore.
	if err := same(); err != nil {
		return "", err
	}
	listed, err := git.Output(ctx, append([]string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--"}, commit.Paths...)...)
	if err != nil {
		return "", describe(err)
	}
	var add []string
	for name := range strings.SplitSeq(string(listed), "\x00") {
		if name != "" {
			add = append(add, name)
		}
	}
	if len(add) == 0 {
		return "", errNothingToCommit
	}
	for _, args := range [][]string{append([]string{"add", "--"}, add...), {"commit", "--quiet", "-m", message}} {
		if err := same(); err != nil {
			return "", err
		}
		if _, err := git.Output(ctx, args...); err != nil {
			return "", describe(err)
		}
	}
	return git.Text(ctx, "rev-parse", "--verify", "HEAD")
}

// errNothingToCommit is what a first commit says when no file is left to add: every file the shape writes was in the
// folder before, or git ignores each.
var errNothingToCommit = errors.New("no file to commit: every file the shape writes was in the folder already or is ignored by git")

// DefaultBranch is the branch a new repository at path starts on: the one the global and then the system git
// configuration name as init.defaultBranch, as `git init` reads them and never the configuration of a clone around the
// folder, and main when they name none or name a branch git would refuse.
func (f Folders) DefaultBranch(ctx context.Context, path string) string {
	git := gitcli.Git{Dir: path, Env: f.Env}
	for _, scope := range []string{"--global", "--system"} {
		name, err := git.Text(ctx, "config", scope, "--includes", "--get", "init.defaultBranch")
		if err != nil || name == "" {
			continue
		}
		if _, err := git.Output(ctx, "check-ref-format", "--branch", name); err != nil {
			return "main"
		}
		return name
	}
	return "main"
}

// AddRemote adds the remote named origin. The URL was checked to be one line git reads as an address and not as an
// option.
func (f Folders) AddRemote(ctx context.Context, path, url string) error {
	if _, err := (gitcli.Git{Dir: path, Env: f.Env}).Output(ctx, "remote", "add", "origin", "--", url); err != nil {
		return describe(err)
	}
	return nil
}

// Push pushes the branch to origin and sets it as the branch's upstream. A push that names a credential helper
// clears git's helpers for github.com over https for this one command and names that one, through git's
// configuration in the environment, so no configuration file is written.
func (f Folders) Push(ctx context.Context, path string, push ports.PushSpec) error {
	git := gitcli.Git{Dir: path, Env: f.Env}
	if push.CredentialHelper != "" {
		const key = "credential.https://github.com.helper"
		git.Config = [][2]string{{key, ""}, {key, `!"` + push.CredentialHelper + `" auth git-credential`}}
	}
	if _, err := git.Output(ctx, "push", "--set-upstream", "--", "origin", push.Branch); err != nil {
		return describe(err)
	}
	return nil
}

// Remote reads what a repository says of its origin, asking origin for the branch only when it is named.
func (f Folders) Remote(ctx context.Context, path string) (ports.RemoteState, error) {
	git := gitcli.Git{Dir: path, Env: f.Env}
	var state ports.RemoteState
	state.Origin, _ = git.Text(ctx, "remote", "get-url", "origin")
	state.Branch, _ = git.Text(ctx, "symbolic-ref", "--short", "HEAD")
	state.Head, _ = git.Text(ctx, "rev-parse", "--verify", "HEAD")
	if state.Origin != "" && state.Branch != "" {
		if listed, err := git.Text(ctx, "ls-remote", "--", "origin", "refs/heads/"+state.Branch); err == nil {
			state.RemoteHead, _, _ = strings.Cut(listed, "\t")
		}
	}
	return state, nil
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
