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
	"time"
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

// Feed runs one command with input on its stdin and returns its stdout as it is, for a command that
// answers many questions in one run, such as `cat-file --batch`.
func (g Git) Feed(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	cmd.Env = g.environment()
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// refLockAttempts and refLockPause bound how a fetch that lost a ref's lock tries again.
const (
	refLockAttempts = 3
	refLockPause    = 50 * time.Millisecond
)

// Fetch runs `git fetch` with these arguments. Two fetches in one clone can update the same remote-tracking
// ref at the same moment, as the daemon's intake does when it fetches a registered repository while a claim
// fetches the base branch of the lane it starts, and the one that loses git's lock on the ref fails with
// "cannot lock ref". The other fetch is moving the ref to the commit this one found, so another attempt
// finds the ref current: a lost lock is tried again, up to three attempts 50 ms apart and not past the end
// of ctx, and any other failure is returned at once.
//
// Git has no signal for this but the words it prints, because every failed fetch exits 1.
func (g Git) Fetch(ctx context.Context, args ...string) error {
	return retryRefLock(ctx, func() error {
		_, err := g.Output(ctx, append([]string{"fetch"}, args...)...)
		return err
	})
}

// lostRefLock reports whether a git failure is the loss of a ref's lock, whether another process held the
// lock file or moved the ref between this one reading it and locking it.
func lostRefLock(err error) bool {
	return err != nil && strings.Contains(err.Error(), "cannot lock ref")
}

// retryRefLock runs attempt, and runs it again while it fails for a lost lock, up to refLockAttempts runs.
// It returns the last failure, and stops waiting when ctx ends.
func retryRefLock(ctx context.Context, attempt func() error) error {
	err := attempt()
	for tried := 1; tried < refLockAttempts && lostRefLock(err); tried++ {
		select {
		case <-ctx.Done():
			return err
		case <-time.After(refLockPause):
		}
		err = attempt()
	}
	return err
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
