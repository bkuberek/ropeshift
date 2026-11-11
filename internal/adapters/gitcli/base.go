package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// fetchTimeout bounds a fetch of one branch, because a remote that never answers must not hold the
// claim that asked.
const fetchTimeout = time.Minute

// BaseTip reads where a base branch stands. With a remote it fetches the branch and answers with the
// remote-tracking commit, so a lane starts from what the remote holds and not from a checkout that fell
// behind it. With no remote it answers with the local branch. It never answers with HEAD.
func (w Worktrees) BaseTip(ctx context.Context, repository, branch string) (ports.BaseTip, error) {
	unavailable := func(format string, args ...any) (ports.BaseTip, error) {
		return ports.BaseTip{}, fmt.Errorf("%w: "+format, append([]any{ports.ErrBaseUnavailable}, args...)...)
	}
	if err := ledger.CheckBranch(branch); err != nil {
		return unavailable("%v", err)
	}
	git := w.git(repository)
	remote, err := remoteOf(ctx, git, "")
	if err != nil {
		return unavailable("reading the remotes: %v", err)
	}
	if remote == "" {
		commit, err := git.Commit(ctx, "refs/heads/"+branch)
		if err != nil {
			return unavailable("this repository has no branch %s", branch)
		}
		return ports.BaseTip{Ref: branch, Commit: commit}, nil
	}
	tracking := "refs/remotes/" + remote + "/" + branch
	fetching, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	if _, err := git.Output(fetching, "fetch", "--quiet", "--no-tags", remote, "+refs/heads/"+branch+":"+tracking); err != nil {
		return unavailable("fetching %s from %s: %v", branch, remote, err)
	}
	commit, err := git.Commit(ctx, tracking)
	if err != nil {
		return unavailable("%s holds no branch %s", remote, branch)
	}
	return ports.BaseTip{Ref: remote + "/" + branch, Commit: commit}, nil
}

// Contains reports whether ancestor is reachable from commit. Git exits 1 for a commit that is not, and
// otherwise for one it cannot read, which is a different answer.
func (w Worktrees) Contains(ctx context.Context, repository, ancestor, commit string) (bool, error) {
	git := w.git(repository)
	for _, name := range []string{ancestor, commit} {
		if strings.HasPrefix(name, "-") {
			return false, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
		if _, err := git.Commit(ctx, name); err != nil {
			return false, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
	}
	_, err := git.Output(ctx, "merge-base", "--is-ancestor", ancestor, commit)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, err
}

// Branch reads an in-flight branch a lane continues. The remote's copy comes first, because that is where
// a person's work is shared, and the local branch stands in for a remote that does not hold it, or for a
// repository with none. Neither is moved: a fetch only brings the remote-tracking ref up to date.
func (w Worktrees) Branch(ctx context.Context, repository, branch string) (ports.BaseTip, error) {
	if err := ledger.CheckBranch(branch); err != nil {
		return ports.BaseTip{}, fmt.Errorf("%w: %v", ports.ErrNotFound, err)
	}
	git := w.git(repository)
	remote, err := remoteOf(ctx, git, "")
	if err != nil {
		return ports.BaseTip{}, err
	}
	if remote != "" {
		tracking := "refs/remotes/" + remote + "/" + branch
		fetching, cancel := context.WithTimeout(ctx, fetchTimeout)
		_, fetched := git.Output(fetching, "fetch", "--quiet", "--no-tags", remote, "+refs/heads/"+branch+":"+tracking)
		cancel()
		if fetched == nil {
			if commit, err := git.Commit(ctx, tracking); err == nil {
				return ports.BaseTip{Ref: remote + "/" + branch, Commit: commit}, nil
			}
		}
	}
	commit, err := git.Commit(ctx, "refs/heads/"+branch)
	if err != nil {
		return ports.BaseTip{}, fmt.Errorf("%w: the repository holds no branch %s, on its remote or here", ports.ErrNotFound, branch)
	}
	return ports.BaseTip{Ref: branch, Commit: commit}, nil
}

// MergeBase finds the commit two commits last shared. Git exits 1 for two that share none.
func (w Worktrees) MergeBase(ctx context.Context, repository, a, b string) (string, error) {
	git := w.git(repository)
	for _, name := range []string{a, b} {
		if strings.HasPrefix(name, "-") {
			return "", fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
		if _, err := git.Commit(ctx, name); err != nil {
			return "", fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
	}
	base, err := git.Text(ctx, "merge-base", a, b)
	var exit *exec.ExitError
	switch {
	case err == nil && base != "":
		return base, nil
	case err == nil, errors.As(err, &exit) && exit.ExitCode() == 1:
		return "", fmt.Errorf("%w: %s and %s share no history", ports.ErrNotFound, a, b)
	}
	return "", err
}

// Changes reads what head changed since base, with renames followed, from git's own listing. A copy is an
// added file, and a change of type is a changed one.
func (w Worktrees) Changes(ctx context.Context, repository, base, head string) ([]planning.FileChange, error) {
	git := w.git(repository)
	for _, name := range []string{base, head} {
		if strings.HasPrefix(name, "-") {
			return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
		if _, err := git.Commit(ctx, name); err != nil {
			return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
	}
	listing, err := git.Output(ctx, "diff", "--name-status", "-z", "--no-ext-diff", "-M", base, head, "--")
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimSuffix(string(listing), "\x00"), "\x00")
	changes := []planning.FileChange{}
	for i := 0; i < len(fields) && fields[i] != ""; {
		status := fields[i]
		paths := 1
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			paths = 2
		}
		if i+paths >= len(fields) {
			return nil, fmt.Errorf("git diff --name-status ended part way through %q", status)
		}
		change := planning.FileChange{Path: fields[i+paths]}
		switch status[0] {
		case 'A', 'C':
			change.Status = planning.ChangeAdded
		case 'D':
			change.Status = planning.ChangeDeleted
		case 'R':
			change.Status, change.From = planning.ChangeMoved, fields[i+1]
		default:
			change.Status = planning.ChangeChanged
		}
		changes = append(changes, change)
		i += paths + 1
	}
	return changes, nil
}

var _ ports.Bases = Worktrees{}
