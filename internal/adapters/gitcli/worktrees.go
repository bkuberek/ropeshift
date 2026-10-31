package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ports"
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
	status, err := git.Text(ctx, "status", "--porcelain")
	if err != nil {
		return ports.Worktree{}, err
	}
	return ports.Worktree{Path: path, Branch: branch, Head: head, Clean: status == ""}, nil
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

var _ ports.Worktrees = Worktrees{}
