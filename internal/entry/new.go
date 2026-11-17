package entry

import (
	"context"
	"errors"
	"os/exec"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
)

// cloneTop is the top of the git work tree that holds dir, as `git -C <dir> rev-parse --show-toplevel` names
// it, or empty when no clone holds it or git is missing, which `ropeshift new` reads alike.
func cloneTop(ctx context.Context, dir string) (string, error) {
	top, err := gitcli.Git{}.Text(ctx, "-C", dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", nil
	}
	return top, nil
}

// cloneIgnores says whether the clone at top ignores path, as `git -C <top> check-ignore -q -- <path>`
// answers: exit 0 ignores it and exit 1 does not. Any other answer is an error.
func cloneIgnores(ctx context.Context, top, path string) (bool, error) {
	_, err := gitcli.Git{}.Output(ctx, "-C", top, "check-ignore", "-q", "--", path)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, err
}
