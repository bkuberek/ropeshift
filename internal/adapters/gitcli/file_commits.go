package gitcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ports"
)

// recentCommits is how many commits Commits reads when it has no base to stop at.
const recentCommits = 50

// commitEnvironment runs a commit with none of the repository's code and none of its settings for signing: no hook,
// no signature, and the product's name for both the author and the committer, so a machine with no git identity
// commits all the same.
var commitEnvironment = slices.Concat(snapshotIdentity, []string{
	"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=" + os.DevNull,
	"GIT_CONFIG_KEY_1=commit.gpgsign", "GIT_CONFIG_VALUE_1=false",
})

// plainNames has git print a path as it is, and not in quotes with escapes.
var plainNames = []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.quotePath", "GIT_CONFIG_VALUE_0=false"}

// CommitFiles adds the files to a worktree and commits what that changed. A file whose text is given is written
// only when nothing is at its path, a line is added only to a file with no line like it, and a file already there
// with the text the worker would write is committed all the same, because a crash may have written it and gone
// before the commit. When nothing is left to commit, no commit is made.
func (w Worktrees) CommitFiles(ctx context.Context, worktree string, files []ports.TreeFile, subject string) (ports.Commit, error) {
	if _, err := os.Stat(filepath.Join(worktree, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ports.Commit{}, fmt.Errorf("worktree %s: %w", worktree, ports.ErrNotFound)
		}
		return ports.Commit{}, err
	}
	var staged []string
	for _, file := range files {
		if err := checkTreePath(file.Path); err != nil {
			return ports.Commit{}, err
		}
		stage, err := place(worktree, file)
		if err != nil {
			return ports.Commit{}, err
		}
		if stage {
			staged = append(staged, file.Path)
		}
	}
	if len(staged) == 0 {
		return ports.Commit{}, nil
	}
	git := w.git(worktree)
	git.Extra = commitEnvironment
	if _, err := git.Output(ctx, append([]string{"add", "--"}, staged...)...); err != nil {
		return ports.Commit{}, err
	}
	if changed, err := hasStaged(ctx, git); err != nil || !changed {
		return ports.Commit{}, err
	}
	if _, err := git.Output(ctx, append([]string{"commit", "--quiet", "--no-verify", "-m", subject, "--"}, staged...)...); err != nil {
		return ports.Commit{}, err
	}
	hash, err := git.Text(ctx, "rev-parse", "HEAD")
	if err != nil {
		return ports.Commit{}, err
	}
	listing, err := git.Output(ctx, "diff-tree", "--no-commit-id", "--name-only", "-r", "--root", "-z", hash)
	if err != nil {
		return ports.Commit{}, err
	}
	return ports.Commit{Hash: hash, Subject: subject, Files: splitNames(listing)}, nil
}

// hasStaged says the index differs from HEAD. `git diff --cached --quiet` exits 1 for a difference, and any other
// failure is git not answering.
func hasStaged(ctx context.Context, git Git) (bool, error) {
	_, err := git.Output(ctx, "diff", "--cached", "--quiet")
	var exit *exec.ExitError
	switch {
	case err == nil:
		return false, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return true, nil
	}
	return false, err
}

// splitNames reads a list of names separated by NUL.
func splitNames(listing []byte) []string {
	var names []string
	for name := range strings.SplitSeq(string(listing), "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// checkTreePath refuses a path that is not a clean path below the worktree's top, or that leads into git's own
// directory.
func checkTreePath(name string) error {
	first, _, _ := strings.Cut(name, "/")
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") ||
		first == ".git" || strings.ContainsAny(name, "\\\x00") {
		return fmt.Errorf("%q is no path below the worktree's top", name)
	}
	return nil
}

// place writes one file to the worktree as the rules of TreeFile say, and says whether it is for the commit.
func place(worktree string, file ports.TreeFile) (bool, error) {
	target := filepath.Join(worktree, filepath.FromSlash(file.Path))
	if err := refuseLinks(worktree, file.Path); err != nil {
		return false, err
	}
	held, err := os.ReadFile(target)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return false, err
	}
	if len(file.Lines) != 0 {
		text, changed := withLines(string(held), file.Lines)
		if !changed && missing {
			return false, nil
		}
		if changed {
			if err := writeAtomic(target, []byte(text)); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	switch {
	case missing:
		if err := writeAtomic(target, []byte(file.Text)); err != nil {
			return false, err
		}
		return true, nil
	case string(held) == file.Text:
		// What a crash left, or what the commit already holds: staging it changes nothing the second time.
		return true, nil
	}
	return false, nil
}

// refuseLinks refuses a path whose file or whose folders are a symbolic link, because a write through one would
// leave the worktree.
func refuseLinks(worktree, name string) error {
	at := worktree
	for part := range strings.SplitSeq(name, "/") {
		at = filepath.Join(at, part)
		info, err := os.Lstat(at)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil
		case err != nil:
			return err
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("%s is a symbolic link, and a restructuring writes through none", at)
		}
	}
	return nil
}

// withLines adds each line the text does not hold, each on a line of its own, and says whether it added any.
func withLines(text string, lines []string) (string, bool) {
	held := map[string]bool{}
	for line := range strings.SplitSeq(text, "\n") {
		held[strings.TrimSuffix(line, "\r")] = true
	}
	var missing []string
	for _, line := range lines {
		if !held[line] {
			missing = append(missing, line)
			held[line] = true
		}
	}
	if len(missing) == 0 {
		return text, false
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + strings.Join(missing, "\n") + "\n", true
}

// writeAtomic makes a file appear whole or not at all, so a crash leaves nothing a retry would take for the
// worker's own.
func writeAtomic(target string, content []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	scratch, err := os.CreateTemp(dir, ".restructure.*")
	if err != nil {
		return err
	}
	defer os.Remove(scratch.Name())
	if _, err := scratch.Write(content); err != nil {
		_ = scratch.Close()
		return err
	}
	if err := scratch.Chmod(0o644); err != nil {
		_ = scratch.Close()
		return err
	}
	if err := scratch.Close(); err != nil {
		return err
	}
	return os.Rename(scratch.Name(), target)
}

// Commits lists the commits the worktree's branch holds beyond base, with the files each changed, newest first.
func (w Worktrees) Commits(ctx context.Context, worktree, base string) ([]ports.Commit, error) {
	if _, err := os.Stat(filepath.Join(worktree, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("worktree %s: %w", worktree, ports.ErrNotFound)
		}
		return nil, err
	}
	git := w.git(worktree)
	git.Extra = plainNames
	args := []string{"log", "--no-color", "--name-only", "--format=%x01%H%x1f%s"}
	if base == "" {
		args = append(args, "-n", fmt.Sprint(recentCommits), "HEAD", "--")
	} else {
		if strings.HasPrefix(base, "-") {
			return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, base)
		}
		if _, err := git.Commit(ctx, base); err != nil {
			return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, base)
		}
		args = append(args, base+"..HEAD", "--")
	}
	out, err := git.Output(ctx, args...)
	if err != nil {
		return nil, err
	}
	var commits []ports.Commit
	for block := range bytes.SplitSeq(out, []byte{0x01}) {
		head, names, _ := strings.Cut(string(block), "\n")
		hash, subject, found := strings.Cut(head, "\x1f")
		if !found {
			continue
		}
		commit := ports.Commit{Hash: hash, Subject: subject}
		for name := range strings.SplitSeq(names, "\n") {
			if name != "" {
				commit.Files = append(commit.Files, name)
			}
		}
		commits = append(commits, commit)
	}
	return commits, nil
}

var _ ports.FileCommits = Worktrees{}
