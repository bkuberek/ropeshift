package gitcli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ports"
)

// BranchCommits lists the commits on head's first-parent line that base does not hold, newest first, with no
// files: the history a merge brings in is another repository's, so only the branch's own line is read.
func (w Worktrees) BranchCommits(ctx context.Context, repository, base, head string) ([]ports.Commit, error) {
	git := w.git(repository)
	git.Extra = plainNames
	args := []string{"log", "--no-color", "--first-parent", "-n", fmt.Sprint(recentCommits), "--format=%x01%H%x1f%P%x1f%s"}
	for _, name := range []string{base, head} {
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "-") {
			return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
		if _, err := git.Commit(ctx, name); err != nil {
			return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
	}
	if head == "" {
		return nil, fmt.Errorf("%w: no head", ports.ErrNotFound)
	}
	if base == "" {
		args = append(args, head, "--")
	} else {
		args = append(args, base+".."+head, "--")
	}
	out, err := git.Output(ctx, args...)
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

// CommitPaths lists every path a commit holds, sorted.
func (w Worktrees) CommitPaths(ctx context.Context, repository, commit string) ([]string, error) {
	git := w.git(repository)
	git.Extra = plainNames
	if strings.HasPrefix(commit, "-") {
		return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, commit)
	}
	if _, err := git.Commit(ctx, commit); err != nil {
		return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, commit)
	}
	out, err := git.Output(ctx, "ls-tree", "-r", "-z", "--name-only", "--full-tree", commit)
	if err != nil {
		return nil, err
	}
	var paths []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name != "" {
			paths = append(paths, name)
		}
	}
	slices.Sort(paths)
	return paths, nil
}

var _ ports.BranchImports = Worktrees{}
