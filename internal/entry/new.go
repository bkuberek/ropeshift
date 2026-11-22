package entry

import (
	"context"
	"errors"
	"os/exec"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
)

// cloneGit runs git in a clone `ropeshift new` does not trust: with no hook, no file system monitor, no lazy fetch of
// a missing object, and no transport protocol, so a program the clone's configuration names never runs when `new`
// reads the clone.
func cloneGit() gitcli.Git {
	return gitcli.Git{
		Config: append(gitcli.NoHooks(), [2]string{"core.fsmonitor", "false"}),
		Extra:  []string{gitcli.NoLazyFetch, "GIT_ALLOW_PROTOCOL="},
	}
}

// cloneTop is the top of the git work tree that holds dir, as `git -C <dir> rev-parse --show-toplevel` names
// it, or empty when no clone holds it or git is missing, which `ropeshift new` reads alike.
func cloneTop(ctx context.Context, dir string) (string, error) {
	top, err := cloneGit().Text(ctx, "-C", dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", nil
	}
	return top, nil
}

// cloneIgnores says whether the clone at top ignores path, as `git -C <top> check-ignore -q -- <path>`
// answers: exit 0 ignores it and exit 1 does not. Any other answer is an error.
func cloneIgnores(ctx context.Context, top, path string) (bool, error) {
	_, err := cloneGit().Output(ctx, "-C", top, "check-ignore", "-q", "--", path)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, err
}
