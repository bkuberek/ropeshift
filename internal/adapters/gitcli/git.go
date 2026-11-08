// Package gitcli runs the git command line. It is the one place that knows how the
// product talks to git.
package gitcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrNoRevision means a ref or path does not exist in the repository.
var ErrNoRevision = errors.New("no such revision")

// Git runs commands in one directory. Env replaces the process environment when it
// is set, which is how a test keeps its own configuration.
type Git struct {
	Dir string
	Env []string
	// Extra adds variables after everything else, which is how one command gets an index
	// of its own.
	Extra []string
}

// inheritedLocation lists what would point a command at another repository, or at
// another index, when the daemon started inside a git hook.
var inheritedLocation = []string{
	"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_COMMON_DIR=", "GIT_OBJECT_DIRECTORY=", "GIT_PREFIX=",
}

func (g Git) environment() []string {
	base := g.Env
	if base == nil {
		for _, entry := range os.Environ() {
			inherited := false
			for _, prefix := range inheritedLocation {
				inherited = inherited || strings.HasPrefix(entry, prefix)
			}
			if !inherited {
				base = append(base, entry)
			}
		}
	}
	// A command never waits on a prompt, because a daemon has no terminal.
	return append(append(base[:len(base):len(base)], "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"), g.Extra...)
}

// Output runs one command and returns its stdout as it is.
func (g Git) Output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	cmd.Env = g.environment()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Text runs one command and returns its stdout with the last newline removed.
func (g Git) Text(ctx context.Context, args ...string) (string, error) {
	out, err := g.Output(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// Commit resolves a ref to its commit. A ref that does not exist is ErrNoRevision.
func (g Git) Commit(ctx context.Context, ref string) (string, error) {
	cmd := []string{"rev-parse", "--verify", "--quiet", ref + "^{commit}"}
	out, err := g.Text(ctx, cmd...)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoRevision, ref)
	}
	return out, nil
}
