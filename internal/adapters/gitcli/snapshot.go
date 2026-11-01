package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// snapshotIndex is the file a snapshot builds in, inside the worktree's own git directory.
// A crash leaves it where the next snapshot starts over, and it goes with the worktree.
const snapshotIndex = "ropeshift-snapshot-index"

// preferredRemote is the name a clone gives the place it came from.
const preferredRemote = "origin"

// snapshotIdentity lets a snapshot save work on a machine with no git identity, and says
// plainly what made the commit.
var snapshotIdentity = []string{
	"GIT_AUTHOR_NAME=ropeshift", "GIT_AUTHOR_EMAIL=ropeshift@localhost",
	"GIT_COMMITTER_NAME=ropeshift", "GIT_COMMITTER_EMAIL=ropeshift@localhost",
}

// Survey reads a worktree for a stop. It takes no lock a session could meet.
func (w Worktrees) Survey(ctx context.Context, path string) (lanes.WorktreeFindings, error) {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return lanes.WorktreeFindings{}, fmt.Errorf("worktree %s: %w", path, ports.ErrNotFound)
		}
		return lanes.WorktreeFindings{}, err
	}
	git := w.git(path)
	head, err := git.Text(ctx, "rev-parse", "HEAD")
	if err != nil {
		return lanes.WorktreeFindings{}, err
	}
	// A detached head has no symbolic name, which is an answer and not a failure.
	branch, _ := git.Text(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	status, err := readStatus(ctx, git)
	if err != nil {
		return lanes.WorktreeFindings{}, err
	}
	remote, err := remoteOf(ctx, git, branch)
	if err != nil {
		return lanes.WorktreeFindings{}, err
	}
	ahead, err := commitsAhead(ctx, git, remote, branch)
	if err != nil {
		return lanes.WorktreeFindings{}, err
	}
	return lanes.WorktreeFindings{Branch: branch, Head: head, Dirty: status != "", Remote: remote, Ahead: ahead}, nil
}

// remoteOf finds where a push goes: the branch's upstream, then the usual name, then the
// first there is.
func remoteOf(ctx context.Context, git Git, branch string) (string, error) {
	listed, err := git.Text(ctx, "remote")
	if err != nil {
		return "", err
	}
	remotes := strings.Fields(listed)
	if len(remotes) == 0 {
		return "", nil
	}
	if branch != "" {
		upstream, err := git.Text(ctx, "config", "--get", "branch."+branch+".remote")
		if err == nil && slices.Contains(remotes, upstream) {
			return upstream, nil
		}
	}
	if slices.Contains(remotes, preferredRemote) {
		return preferredRemote, nil
	}
	slices.Sort(remotes)
	return remotes[0], nil
}

// commitsAhead counts what only this branch holds. With a remote, that is what the remote's
// refs here do not reach, and with none, what no other branch reaches.
func commitsAhead(ctx context.Context, git Git, remote, branch string) (int64, error) {
	args := []string{"rev-list", "--count", "HEAD", "--not"}
	if remote != "" {
		args = append(args, "--remotes="+remote)
	} else {
		if branch != "" {
			args = append(args, "--exclude="+branch)
		}
		args = append(args, "--branches")
	}
	text, err := git.Text(ctx, args...)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(text, 10, 64)
}

// Snapshot builds a commit from the worktree in an index of its own, because `git stash
// create` omits untracked files and the lane's own index belongs to the session.
func (w Worktrees) Snapshot(ctx context.Context, path, ref string) (string, error) {
	git := w.git(path)
	file, err := git.Text(ctx, "rev-parse", "--path-format=absolute", "--git-path", snapshotIndex)
	if err != nil {
		return "", err
	}
	for _, stale := range []string{file, file + ".lock"} {
		if err := os.Remove(stale); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	defer os.Remove(file)
	index := git
	index.Extra = snapshotIndexVariable(file)
	if _, err := index.Output(ctx, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	crashpoint.Hit("worktree.snapshot", "read")
	if _, err := index.Output(ctx, snapshotAdd()...); err != nil {
		return "", err
	}
	crashpoint.Hit("worktree.snapshot", "added")
	tree, err := index.Text(ctx, "write-tree")
	if err != nil {
		return "", err
	}
	head, err := git.Commit(ctx, "HEAD")
	if err != nil {
		return "", err
	}
	previous, err := git.Commit(ctx, ref)
	hasPrevious := err == nil
	if hasPrevious {
		held, err := git.Text(ctx, "rev-parse", previous+"^{tree}")
		if err != nil {
			return "", err
		}
		if held == tree && w.isAncestor(ctx, git, head, previous) {
			return previous, nil
		}
	}
	args := []string{"commit-tree", tree, "-p", head, "-m", "ropeshift snapshot"}
	if hasPrevious {
		args = append(args, "-p", previous)
	}
	stamp := git
	stamp.Extra = snapshotIdentity
	commit, err := stamp.Text(ctx, args...)
	if err != nil {
		return "", err
	}
	crashpoint.Hit("worktree.snapshot", "committed")
	if _, err := git.Output(ctx, "update-ref", ref, commit); err != nil {
		return "", err
	}
	crashpoint.Hit("worktree.snapshot", "recorded")
	return commit, nil
}

func (w Worktrees) isAncestor(ctx context.Context, git Git, ancestor, of string) bool {
	_, err := git.Output(ctx, "merge-base", "--is-ancestor", ancestor, of)
	return err == nil
}

// Push sends one ref under its own name. It runs no repository hook, because a stop must
// not depend on code in the repository it is saving, and it never forces.
func (w Worktrees) Push(ctx context.Context, path, remote, ref string) error {
	if remote == "" {
		return fmt.Errorf("no remote to push %s to", ref)
	}
	_, err := w.git(path).Output(ctx, pushArguments(remote, ref)...)
	return err
}
