package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// Worktrees manages git worktrees with the git command line. Env replaces the
// environment of every command, which is how a test keeps its own configuration.
type Worktrees struct {
	Env []string
}

func (w Worktrees) git(dir string) Git { return Git{Dir: dir, Env: w.Env} }

// listed is one entry of `git worktree list --porcelain`.
type listed struct {
	path   string
	branch string
}

func (w Worktrees) list(ctx context.Context, repository string) ([]listed, error) {
	out, err := w.git(repository).Text(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var entries []listed
	for block := range strings.SplitSeq(out, "\n\n") {
		var entry listed
		for line := range strings.SplitSeq(block, "\n") {
			if path, ok := strings.CutPrefix(line, "worktree "); ok {
				entry.path = path
			}
			if branch, ok := strings.CutPrefix(line, "branch refs/heads/"); ok {
				entry.branch = branch
			}
		}
		if entry.path != "" {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// canonical resolves symlinks in the part of a path that exists, because git reports
// a worktree's real path and a temporary directory is often behind a link.
func canonical(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(canonical(parent), filepath.Base(path))
}

// Create makes a worktree. It prunes first, so a worktree whose directory went away
// in a crash does not hold its branch, and an existing match is the answer to a
// retry.
func (w Worktrees) Create(ctx context.Context, spec ports.WorktreeSpec) (ports.Worktree, error) {
	repository := w.git(spec.Repository)
	if prunesBeforeCreate() {
		if _, err := repository.Output(ctx, "worktree", "prune"); err != nil {
			return ports.Worktree{}, err
		}
	}
	crashpoint.Hit("worktree.create", "pruned")
	entries, err := w.list(ctx, spec.Repository)
	if err != nil {
		return ports.Worktree{}, err
	}
	want := canonical(spec.Path)
	for _, entry := range entries {
		switch {
		case entry.path == want && entry.branch == spec.Branch:
			return w.Inspect(ctx, spec.Path)
		case entry.path == want:
			return ports.Worktree{}, fmt.Errorf("%w: %s holds %s", ports.ErrWorktreeConflict, spec.Path, entry.branch)
		case entry.branch == spec.Branch:
			return ports.Worktree{}, fmt.Errorf("%w: %s is checked out at %s", ports.ErrWorktreeConflict, spec.Branch, entry.path)
		}
	}
	if err := refuseOccupied(spec.Path); err != nil {
		return ports.Worktree{}, err
	}
	if err := os.MkdirAll(filepath.Dir(spec.Path), 0o700); err != nil {
		return ports.Worktree{}, err
	}
	args := []string{"worktree", "add", "--quiet"}
	if _, err := repository.Commit(ctx, "refs/heads/"+spec.Branch); err == nil {
		args = append(args, spec.Path, spec.Branch)
	} else {
		base := spec.Base
		if base == "" {
			base = "HEAD"
		}
		args = append(args, "-b", spec.Branch, spec.Path, base)
	}
	if _, err := repository.Output(ctx, args...); err != nil {
		return ports.Worktree{}, err
	}
	crashpoint.Hit("worktree.create", "added")
	return w.Inspect(ctx, spec.Path)
}

// refuseOccupied leaves a path that holds someone's files alone. An empty directory
// is what a crash before the checkout leaves, and git takes it.
func refuseOccupied(path string) error {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%w: %s holds files", ports.ErrWorktreeConflict, path)
	}
	return nil
}

// Inspect reads a worktree.
func (w Worktrees) Inspect(ctx context.Context, path string) (ports.Worktree, error) {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return ports.Worktree{}, fmt.Errorf("worktree %s: %w", path, ports.ErrNotFound)
	}
	git := w.git(path)
	head, err := git.Text(ctx, "rev-parse", "HEAD")
	if err != nil {
		return ports.Worktree{}, err
	}
	branch, err := git.Text(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ports.Worktree{}, err
	}
	status, err := readStatus(ctx, git)
	if err != nil {
		return ports.Worktree{}, err
	}
	return ports.Worktree{Path: path, Branch: branch, Head: head, Clean: status == ""}, nil
}

// readStatus reads what differs from HEAD. Status refreshes the index when it can, which a
// reader must not do to a worktree a session is writing in.
func readStatus(ctx context.Context, git Git) (string, error) {
	return git.Text(ctx, "--no-optional-locks", "status", "--porcelain")
}

// Remove deletes a clean worktree. Git refuses a dirty one on its own, and this
// checks first so the refusal is a typed answer. The branch stays, because a lane's
// work outlives its directory.
func (w Worktrees) Remove(ctx context.Context, repository, path string) error {
	current, err := w.Inspect(ctx, path)
	if errors.Is(err, ports.ErrNotFound) {
		_, err := w.git(repository).Output(ctx, "worktree", "prune")
		return err
	}
	if err != nil {
		return err
	}
	if !current.Clean {
		return fmt.Errorf("%w: %s", ports.ErrWorktreeDirty, path)
	}
	_, err = w.git(repository).Output(ctx, "worktree", "remove", path)
	return err
}

// Discard deletes a worktree whatever it holds, and keeps its branch. Only a caller that
// has saved the work, or a member who said to lose it, gets here.
func (w Worktrees) Discard(ctx context.Context, repository, path string) error {
	if _, err := os.Stat(filepath.Join(path, ".git")); errors.Is(err, os.ErrNotExist) {
		_, err := w.git(repository).Output(ctx, "worktree", "prune")
		return err
	}
	_, err := w.git(repository).Output(ctx, "worktree", "remove", "--force", path)
	return err
}

// DefaultBranch reads a remote's HEAD where it has one, and a checkout's HEAD otherwise.
func (w Worktrees) DefaultBranch(ctx context.Context, repository string) (string, error) {
	git := w.git(repository)
	if head, err := git.Text(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && head != "" {
		return strings.TrimPrefix(head, "origin/"), nil
	}
	return git.Text(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
}

// contextIgnore is what keeps a pack's directory out of git. It lives in that directory, so it
// needs no entry in a file a repository or its other worktrees share.
const contextIgnore = "*\n"

// WriteContext leaves a pack's text where a session reads it. A rename means a session never reads
// half a pack, and the directory ignores itself, so git sees nothing there.
func (w Worktrees) WriteContext(_ context.Context, path, text string) (string, error) {
	file := filepath.Join(path, lanes.PackFile)
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(contextIgnore), 0o600); err != nil {
		return "", err
	}
	scratch, err := os.CreateTemp(dir, "pack-*.md")
	if err != nil {
		return "", err
	}
	defer os.Remove(scratch.Name())
	if _, err := scratch.WriteString(text); err != nil {
		_ = scratch.Close()
		return "", err
	}
	if err := scratch.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(scratch.Name(), file); err != nil {
		return "", err
	}
	return file, nil
}

var _ ports.Worktrees = Worktrees{}
