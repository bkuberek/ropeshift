package gitcli

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// NoLazyFetch is the variable that makes git 2.44 and later fail on a missing object of a partial clone instead of
// fetching it from the promisor remote. An older git ignores it, so a caller that must not fetch asks PartialClone and
// reads no object the clone may lack.
const NoLazyFetch = "GIT_NO_LAZY_FETCH=1"

// PartialClone reports whether the repository is a partial clone: `extensions.partialClone` is set, or a remote is a
// promisor (`remote.<name>.promisor` is true). A partial clone lacks objects that git fetches from its promisor remote
// when a command reads one, so a command that must stay offline must not read an object of a repository that answers
// true.
func (g Git) PartialClone(ctx context.Context) (bool, error) {
	extension, err := g.configLines(ctx, "--get", "extensions.partialclone")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(extension) != "" {
		return true, nil
	}
	promisors, err := g.configLines(ctx, "--type=bool", "--get-regexp", `^remote\..+\.promisor$`)
	if err != nil {
		return false, err
	}
	for line := range strings.SplitSeq(promisors, "\n") {
		// A line is the key, a space, and the value, and a remote's name may hold a space, so the value follows the last one.
		if cut := strings.LastIndexByte(line, ' '); cut >= 0 && line[cut+1:] == "true" {
			return true, nil
		}
	}
	return false, nil
}

// configLines runs `git config` with the arguments and returns its output. A setting that is not set is empty and no
// error, because `git config` exits 1 for it.
func (g Git) configLines(ctx context.Context, args ...string) (string, error) {
	out, err := g.Output(ctx, append([]string{"config"}, args...)...)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil
	}
	return string(out), err
}
