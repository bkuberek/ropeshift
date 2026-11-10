package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
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

var _ ports.Bases = Worktrees{}
