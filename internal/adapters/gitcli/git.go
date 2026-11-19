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
	"strconv"
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
	// Config adds settings, each a key and a value, through git's configuration in the environment after any the
	// environment already holds, so the environment's own settings stand.
	Config [][2]string
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
	env := append(append(base[:len(base):len(base)], "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"), g.Extra...)
	for _, setting := range g.Config {
		env = withGitConfig(env, setting[0], setting[1])
	}
	return withGitConfig(env, "maintenance.auto", "false")
}

// withGitConfig adds one configuration setting to a command's environment, after any the environment already
// holds, through git's configuration in the environment
// (https://git-scm.com/docs/git-config#ENVIRONMENT). It never writes a configuration file, so it leaves the
// repository's and the person's own configuration as they are.
//
// Every command turns auto maintenance off, because the `git worktree prune` that maintenance runs in a
// process of its own removes a registration that a worktree add in progress has not finished writing.
func withGitConfig(env []string, key, value string) []string {
	count := 0
	for _, entry := range env {
		if text, ok := strings.CutPrefix(entry, "GIT_CONFIG_COUNT="); ok {
			if n, err := strconv.Atoi(text); err == nil && n >= 0 {
				count = n
			} else {
				count = 0
			}
		}
	}
	n := strconv.Itoa(count)
	return append(env[:len(env):len(env)],
		"GIT_CONFIG_KEY_"+n+"="+key, "GIT_CONFIG_VALUE_"+n+"="+value, "GIT_CONFIG_COUNT="+strconv.Itoa(count+1))
}

// waitDelay is how long a command that was stopped by its context may keep its output open before the call returns. A helper
// git started, such as ssh, can hold the pipes past the end of git itself, and the call returns at the deadline all the same.
const waitDelay = time.Second

// Output runs one command and returns its stdout as it is.
func (g Git) Output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	cmd.Env = g.environment()
	cmd.WaitDelay = waitDelay
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
	cmd.WaitDelay = waitDelay
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// refLockAttempts and refLockPause bound how a command that lost a race with another git process tries again,
// as a fetch does for a ref's lock and a worktree add does for a registration.
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
	return retryWhile(ctx, lostRefLock, attempt)
}

// retryWhile runs attempt, and runs it again while it fails with an error that again accepts, up to
// refLockAttempts runs with refLockPause between them. It returns the last failure, and stops waiting when
// ctx ends.
func retryWhile(ctx context.Context, again func(error) bool, attempt func() error) error {
	err := attempt()
	for tried := 1; tried < refLockAttempts && again(err); tried++ {
		select {
		case <-ctx.Done():
			return err
		case <-time.After(refLockPause):
		}
		err = attempt()
	}
	return err
}

// errRegistrationLost is a create's own finding that the registration its add made is gone or broken,
// although the add succeeded.
var errRegistrationLost = errors.New("the worktree's registration was lost")

// lostRegistration reports whether a git failure is a worktree registration that vanished under the command
// that was making it. `git worktree add` makes the entry under .git/worktrees and then writes its files one by
// one, and a `git worktree prune` in another process, such as the one git's auto maintenance runs, removes an
// entry that has neither locked nor gitdir yet. The prune decides first and removes the entry file by file
// after, so the add may already have written more by then. The add then fails making the entry, opening one
// of the files it writes, reading the entry back, or checking out through it. The adapter's lock does not
// hold that process back. A prune that removed the last entry also removes .git/worktrees itself.
//
// Git has no signal for this but the words it prints, because every failed command exits 128. An open fails
// with ENOENT on Linux, and with EINVAL on macOS while the prune is removing the directory.
func lostRegistration(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errRegistrationLost) {
		return true
	}
	text := err.Error()
	// Git 2.44 and later print the first when the registration is gone before the add reads it back. Git 2.56
	// and later print the second when a command reads a worktree whose registration is gone, where earlier
	// versions print "not a git repository: " and the registration's path, which the check below reads.
	if strings.Contains(text, "could not find created worktree '") ||
		strings.Contains(text, "gitfile does not point to a valid repository: ") {
		return true
	}
	if !strings.Contains(text, "/worktrees/") {
		return false
	}
	if strings.Contains(text, "not a git repository: ") ||
		(strings.Contains(text, "could not create directory of '") && strings.HasSuffix(text, ": No such file or directory")) {
		return true
	}
	for _, name := range []string{"locked", "gitdir", "commondir"} {
		for _, cause := range []string{"No such file or directory", "Invalid argument"} {
			if strings.Contains(text, "/"+name+"' for writing: "+cause) {
				return true
			}
		}
	}
	return false
}

// retryLostRegistration runs step, which is a whole create of a worktree, and runs it again while it fails
// for a lost registration, whether git reported it or the step found it, up to three runs 50 ms apart and
// not past the end of ctx. Another run prunes what is left, lists again, and adds again, which is what makes
// a create safe to repeat. Any other failure is returned at once.
func retryLostRegistration(ctx context.Context, step func() error) error {
	return retryWhile(ctx, lostRegistration, step)
}

// Text runs one command and returns its stdout with the last newline removed.
func (g Git) Text(ctx context.Context, args ...string) (string, error) {
	out, err := g.Output(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// Commit resolves a ref to its commit. A ref that does not exist is ErrNoRevision. The ref follows
// `--end-of-options`, so a name that reads as an option is never one.
func (g Git) Commit(ctx context.Context, ref string) (string, error) {
	cmd := []string{"rev-parse", "--verify", "--quiet", "--end-of-options", ref + "^{commit}"}
	out, err := g.Text(ctx, cmd...)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoRevision, ref)
	}
	return out, nil
}
