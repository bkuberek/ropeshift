package gitcli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ports"
)

// branchCommits bounds the commits BranchCommits lists, which a branch's own line rarely comes near: an import is the
// first commit after the branch began, and what follows it is one session's work.
const branchCommits = 1000

// resolved names a commit by its hash, refusing a name git would read as an option or one the repository does not
// hold, so a command is given the commit and never a ref that could move or mean something else.
func resolved(ctx context.Context, git Git, name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("%w: %q", ports.ErrNotFound, name)
	}
	hash, err := git.Commit(ctx, name)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ports.ErrNotFound, name)
	}
	return hash, nil
}

// BranchCommits lists the commits on head's first-parent line that base does not hold, newest first, with no
// files: the history a merge brings in is another repository's, so only the branch's own line is read.
func (w Worktrees) BranchCommits(ctx context.Context, repository, base, head string) ([]ports.Commit, error) {
	git := w.git(repository)
	git.Extra = plainNames
	to, err := resolved(ctx, git, head)
	if err != nil {
		return nil, err
	}
	span := to
	if base != "" {
		from, err := resolved(ctx, git, base)
		if err != nil {
			return nil, err
		}
		span = from + ".." + to
	}
	out, err := git.Output(ctx, "log", "--no-color", "--no-show-signature", "--first-parent", "-n", fmt.Sprint(branchCommits),
		"--format=%x01%H%x1f%P%x1f%s", span, "--")
	if err != nil {
		return nil, err
	}
	var commits []ports.Commit
	for block := range strings.SplitSeq(string(out), "\x01") {
		line := strings.TrimSpace(block)
		hash, rest, found := strings.Cut(line, "\x1f")
		parents, subject, joined := strings.Cut(rest, "\x1f")
		if !found || !joined {
			continue
		}
		commits = append(commits, ports.Commit{Hash: hash, Subject: subject, Parents: strings.Fields(parents)})
	}
	return commits, nil
}

// errEnoughPaths stops a listing of a tree that reached its limit.
var errEnoughPaths = errors.New("enough paths")

// CommitPaths lists the paths a commit holds, sorted, at most limit of them, reading git's listing as it comes so a
// tree of any size costs at most the limit.
func (w Worktrees) CommitPaths(ctx context.Context, repository, commit string, limit int) ([]string, bool, error) {
	git := w.git(repository)
	git.Extra = plainNames
	hash, err := resolved(ctx, git, commit)
	if err != nil {
		return nil, false, err
	}
	var paths []string
	truncated := false
	err = git.Stream(ctx, func(out io.Reader) error {
		names := bufio.NewScanner(out)
		names.Buffer(make([]byte, 0, 4096), 1<<20)
		names.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			if at := bytes.IndexByte(data, 0); at >= 0 {
				return at + 1, data[:at], nil
			}
			if atEOF && len(data) > 0 {
				return len(data), data, nil
			}
			return 0, nil, nil
		})
		for names.Scan() {
			if len(paths) == limit {
				truncated = true
				return errEnoughPaths
			}
			if name := names.Text(); name != "" {
				paths = append(paths, name)
			}
		}
		return names.Err()
	}, "ls-tree", "-r", "-z", "--name-only", "--full-tree", hash)
	if err != nil && !errors.Is(err, errEnoughPaths) {
		return nil, false, err
	}
	slices.Sort(paths)
	return paths, truncated, nil
}

// FirstParentRoot is the commit with no parent at the end of a commit's first-parent line.
func (w Worktrees) FirstParentRoot(ctx context.Context, repository, commit string) (string, error) {
	git := w.git(repository)
	hash, err := resolved(ctx, git, commit)
	if err != nil {
		return "", err
	}
	out, err := git.Output(ctx, "rev-list", "--first-parent", "--max-parents=0", hash, "--")
	if err != nil {
		return "", err
	}
	roots := strings.Fields(string(out))
	if len(roots) == 0 {
		return "", fmt.Errorf("%w: %s has no first commit", ports.ErrNotFound, commit)
	}
	return roots[len(roots)-1], nil
}

var _ ports.BranchImports = Worktrees{}
