// Package practicegit reads a repository with the git command line for the practices check. It runs only
// read-only git commands against the checkout, and never reads its working tree.
package practicegit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
)

// Snapshots reads a checkout at one commit with git. Env replaces the process environment of the commands run
// in the checkout when it is set, and nil is this process's.
type Snapshots struct {
	Env []string
}

var _ ports.PracticeSnapshots = Snapshots{}

// snapshotCommitID matches a full commit ID, SHA-1 or SHA-256, which is never a branch name.
var snapshotCommitID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// snapshotIgnoreFile is the name of the file whose committed copies decide the ignore answers.
const snapshotIgnoreFile = ".gitignore"

// Snapshot returns the tracked paths at a commit, a reader of a tracked file's text, and the ignore answer for
// each path of practices.Probes. The commit is a commit ID, or the name of the base branch, which resolves to
// origin's branch when the checkout holds it and to the local branch otherwise. A name that resolves to
// neither is ports.ErrNotFound.
func (s Snapshots) Snapshot(ctx context.Context, dir, commit string) (practices.Snapshot, error) {
	git := gitcli.Git{Dir: dir, Env: s.Env}
	resolved, err := snapshotResolve(ctx, git, commit)
	if err != nil {
		return practices.Snapshot{}, err
	}
	paths, err := snapshotPaths(ctx, git, resolved)
	if err != nil {
		return practices.Snapshot{}, err
	}
	ignored, err := snapshotIgnored(ctx, git, resolved, paths)
	if err != nil {
		return practices.Snapshot{}, err
	}
	return practices.Snapshot{
		Commit:  resolved,
		Paths:   paths,
		Read:    snapshotReader(ctx, git, resolved),
		Ignored: ignored,
	}, nil
}

// snapshotResolve turns a commit ID or a base branch's name into a commit ID.
func snapshotResolve(ctx context.Context, git gitcli.Git, name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("%w: %q is not a commit or a branch", ports.ErrNotFound, name)
	}
	refs := []string{"refs/remotes/origin/" + name, "refs/heads/" + name, name}
	if snapshotCommitID.MatchString(name) {
		refs = []string{name}
	}
	for _, ref := range refs {
		id, err := git.Commit(ctx, ref)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, gitcli.ErrNoRevision) {
			return "", err
		}
	}
	return "", fmt.Errorf("%w: the repository holds no commit or branch %s", ports.ErrNotFound, name)
}

// snapshotPaths lists the tracked paths at a commit.
func snapshotPaths(ctx context.Context, git gitcli.Git, commit string) ([]string, error) {
	out, err := git.Output(ctx, "ls-tree", "-r", "-z", "--name-only", commit)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" {
			paths = append(paths, name)
		}
	}
	return paths, nil
}

// snapshotReader returns a reader that asks git for one blob of the commit per call, so it holds no process
// and needs no close.
func snapshotReader(ctx context.Context, git gitcli.Git, commit string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		out, err := git.Output(ctx, "cat-file", "blob", commit+":"+name)
		if err != nil {
			return "", false
		}
		return string(out), true
	}
}

// snapshotIgnored answers, for each path of practices.Probes, whether the `.gitignore` files committed at the
// commit ignore it. It copies those files into a temporary repository of their own and asks git there, so
// the working tree, the person's global excludes, and the checkout's `info/exclude` never count. The
// temporary repository is gone when this returns.
func snapshotIgnored(ctx context.Context, git gitcli.Git, commit string, paths []string) (map[string]bool, error) {
	probes := practices.Probes(paths)
	ignored := make(map[string]bool, len(probes))
	var ignoreFiles []string
	for _, name := range paths {
		if path.Base(name) == snapshotIgnoreFile {
			ignoreFiles = append(ignoreFiles, name)
		}
	}
	if len(ignoreFiles) == 0 {
		for _, probe := range probes {
			ignored[probe] = false
		}
		return ignored, nil
	}

	root, err := os.MkdirTemp("", "ropeshift-practice-ignore-")
	if err != nil {
		return nil, fmt.Errorf("making a repository to check ignore rules in: %w", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	scratch := gitcli.Git{Dir: root, Env: snapshotIsolatedEnv(root)}
	if _, err := scratch.Output(ctx, "init", "--quiet", "--template="); err != nil {
		return nil, err
	}
	for _, name := range ignoreFiles {
		content, err := git.Output(ctx, "cat-file", "blob", commit+":"+name)
		if err != nil {
			return nil, err
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, fmt.Errorf("copying %s: %w", name, err)
		}
		if err := os.WriteFile(target, content, 0o600); err != nil {
			return nil, fmt.Errorf("copying %s: %w", name, err)
		}
	}
	for _, probe := range probes {
		_, err := scratch.Output(ctx, "check-ignore", "--no-index", "-q", "--", snapshotLiteral(probe))
		var exit *exec.ExitError
		switch {
		case err == nil:
			ignored[probe] = true
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			ignored[probe] = false
		default:
			return nil, fmt.Errorf("checking whether %s is ignored: %w", probe, err)
		}
	}
	return ignored, nil
}

// snapshotIsolatedEnv is the whole environment of a command in the temporary repository: no global or system
// configuration, and an empty core.excludesFile, which git reads from its environment as it does `-c`
// (https://git-scm.com/docs/git-config#ENVIRONMENT).
func snapshotIsolatedEnv(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + home,
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.excludesFile",
		"GIT_CONFIG_VALUE_0=",
	}
}

// snapshotLiteral keeps a path that starts with a colon from reading as pathspec magic, which check-ignore refuses.
func snapshotLiteral(name string) string {
	if strings.HasPrefix(name, ":") {
		return "./" + name
	}
	return name
}
