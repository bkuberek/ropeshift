package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// snapshotIndex is the file a snapshot builds in, inside the worktree's own git directory.
// A crash leaves it where the next snapshot starts over, and it goes with the worktree.
const snapshotIndex = "ropeshift-snapshot-index"

// preferredRemote is the name a clone gives the place it came from.
const preferredRemote = "origin"

// snapshotIdentity lets a snapshot save work on a machine with no git identity, and says
// plainly what made the commit.
var snapshotIdentity = []string{
	"GIT_AUTHOR_NAME=ropeshift", "GIT_AUTHOR_EMAIL=ropeshift@localhost",
	"GIT_COMMITTER_NAME=ropeshift", "GIT_COMMITTER_EMAIL=ropeshift@localhost",
}

// Survey reads a worktree for a stop. It takes no lock a session could meet. A worktree made detached
// is for reading and is thrown away, so whatever a session left in it is nothing to account for, and
// the survey says only where it stands.
func (w Worktrees) Survey(ctx context.Context, path string) (lanes.WorktreeFindings, error) {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return lanes.WorktreeFindings{}, fmt.Errorf("worktree %s: %w", path, ports.ErrNotFound)
		}
		return lanes.WorktreeFindings{}, err
	}
	git := w.git(path)
	head, err := git.Text(ctx, "rev-parse", "HEAD")
	if err != nil {
		return lanes.WorktreeFindings{}, err
	}
	if madeDetached(ctx, git) {
		return lanes.WorktreeFindings{Head: head}, nil
	}
	// A detached head has no symbolic name, which is an answer and not a failure.
	branch, _ := git.Text(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	changes, err := statusEntries(ctx, git, "all")
	if err != nil {
		return lanes.WorktreeFindings{}, err
	}
	folders, err := untrackedFolders(ctx, git, changes)
	if err != nil {
		return lanes.WorktreeFindings{}, err
	}
	remote, err := remoteOf(ctx, git, branch)
	if err != nil {
		return lanes.WorktreeFindings{}, err
	}
	ahead, err := commitsAhead(ctx, git, remote, branch)
	if err != nil {
		return lanes.WorktreeFindings{}, err
	}
	found := lanes.WorktreeFindings{Branch: branch, Head: head, Dirty: len(changes) != 0, Folders: folders, Remote: remote, Ahead: ahead}
	for _, change := range changes {
		found.DirtyPaths = append(found.DirtyPaths, change.path)
	}
	slices.Sort(found.DirtyPaths)
	return found, nil
}

// statusColumns is what comes before a path in an entry of `git status --porcelain`: two
// letters and a space.
const statusColumns = 3

// statusEntry is one entry of `git status --porcelain -z`: its two letters and its path, as git holds it.
type statusEntry struct {
	code string
	path string
}

// statusEntries reads `git status` in the form whose names are never quoted, with a move read as the path gone
// and the path added, so each entry names one path exactly. Untracked is git's untracked-files mode: `all` names
// every file in an untracked folder, and `normal` names the folder.
func statusEntries(ctx context.Context, git Git, untracked string) ([]statusEntry, error) {
	out, err := git.Output(ctx, "--no-optional-locks", "status", "--porcelain", "-z", "--no-renames", "--untracked-files="+untracked)
	if err != nil {
		return nil, err
	}
	var entries []statusEntry
	for entry := range strings.SplitSeq(string(out), "\x00") {
		if len(entry) > statusColumns {
			entries = append(entries, statusEntry{code: entry[:statusColumns-1], path: entry[statusColumns:]})
		}
	}
	return entries, nil
}

// untrackedFolders names the folders git tracks nothing in, which only a worktree with an untracked file holds.
func untrackedFolders(ctx context.Context, git Git, changes []statusEntry) ([]string, error) {
	if !slices.ContainsFunc(changes, func(entry statusEntry) bool { return entry.code == "??" }) {
		return nil, nil
	}
	entries, err := statusEntries(ctx, git, "normal")
	if err != nil {
		return nil, err
	}
	var folders []string
	for _, entry := range entries {
		if entry.code == "??" && strings.HasSuffix(entry.path, "/") {
			folders = append(folders, entry.path)
		}
	}
	return folders, nil
}

// remoteOf finds where a push goes: the branch's upstream, then the usual name, then the
// first there is. A remote's name is whatever the repository's configuration says, so one that reads as an
// option to git is refused, where a fetch that carries no `--` would take it for one.
func remoteOf(ctx context.Context, git Git, branch string) (string, error) {
	remote, err := chooseRemote(ctx, git, branch)
	if err == nil && strings.HasPrefix(remote, "-") {
		return "", fmt.Errorf("the remote %q is named like an option, so git is not run with it", remote)
	}
	return remote, err
}

func chooseRemote(ctx context.Context, git Git, branch string) (string, error) {
	listed, err := git.Text(ctx, "remote")
	if err != nil {
		return "", err
	}
	remotes := strings.Fields(listed)
	if len(remotes) == 0 {
		return "", nil
	}
	if branch != "" {
		upstream, err := git.Text(ctx, "config", "--get", "branch."+branch+".remote")
		if err == nil && slices.Contains(remotes, upstream) {
			return upstream, nil
		}
	}
	if slices.Contains(remotes, preferredRemote) {
		return preferredRemote, nil
	}
	slices.Sort(remotes)
	return remotes[0], nil
}

// commitsAhead counts what only this branch holds. With a remote, that is what the remote's
// refs here do not reach, and with none, what no other branch reaches.
func commitsAhead(ctx context.Context, git Git, remote, branch string) (int64, error) {
	args := []string{"rev-list", "--count", "HEAD", "--not"}
	if remote != "" {
		args = append(args, "--remotes="+remote)
	} else {
		if branch != "" {
			args = append(args, "--exclude="+branch)
		}
		args = append(args, "--branches")
	}
	text, err := git.Text(ctx, args...)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(text, 10, 64)
}

// Snapshot builds a commit from the worktree in an index of its own, because `git stash
// create` omits untracked files and the lane's own index belongs to the session.
func (w Worktrees) Snapshot(ctx context.Context, path, ref string) (string, error) {
	return w.snapshot(ctx, path, ref, func(index Git) error {
		_, err := index.Output(ctx, snapshotAdd()...)
		return err
	})
}

// SnapshotPaths builds the same commit from only the named paths, read literally and fed on standard input, so
// no name is taken for an option or a pattern and no list is too long for one command.
func (w Worktrees) SnapshotPaths(ctx context.Context, path, ref string, paths []string) (string, error) {
	return w.snapshot(ctx, path, ref, func(index Git) error {
		if len(paths) == 0 {
			return nil
		}
		addable, err := addablePaths(ctx, index, path, paths)
		if err != nil || len(addable) == 0 {
			return err
		}
		names := []byte(strings.Join(addable, "\x00") + "\x00")
		_, err = index.Feed(ctx, names, "--literal-pathspecs", "add", "--all", "--pathspec-from-file=-", "--pathspec-file-nul")
		return err
	})
}

// addablePaths keeps the paths an add takes, because one it refuses fails the whole add: a path that is in neither
// the worktree nor the snapshot's index, which HEAD filled, one that lies beyond a link, and one git now ignores.
// A path gone from the worktree that the index holds stays, because it is a removal.
func addablePaths(ctx context.Context, index Git, worktree string, paths []string) ([]string, error) {
	var present, gone []string
	for _, path := range paths {
		if beyondLink(worktree, path) {
			continue
		}
		_, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(path)))
		switch {
		case err == nil:
			present = append(present, path)
		case errors.Is(err, os.ErrNotExist):
			gone = append(gone, path)
		default:
			return nil, err
		}
	}
	held, err := indexed(ctx, index, gone)
	if err != nil {
		return nil, err
	}
	ignored, err := ignoredPaths(ctx, index, present)
	if err != nil {
		return nil, err
	}
	var addable []string
	for _, path := range present {
		if !slices.Contains(ignored, path) {
			addable = append(addable, path)
		}
	}
	return append(addable, held...), nil
}

// beyondLink says a path lies past a link: one of the folders above it in the worktree is a link.
func beyondLink(worktree, path string) bool {
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		info, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(strings.Join(parts[:i], "/"))))
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// indexedChunk is how many paths one `ls-files` names, which keeps a command line short.
const indexedChunk = 500

// indexed is what of the paths the index holds.
func indexed(ctx context.Context, index Git, paths []string) ([]string, error) {
	var held []string
	for chunk := range slices.Chunk(paths, indexedChunk) {
		out, err := index.Output(ctx, append([]string{"--literal-pathspecs", "ls-files", "-z", "--cached", "--"}, chunk...)...)
		if err != nil {
			return nil, err
		}
		for name := range strings.SplitSeq(string(out), "\x00") {
			if name != "" {
				held = append(held, name)
			}
		}
	}
	return held, nil
}

// ignoredPaths is what of the paths git ignores. A file the index tracks is never ignored. Git exits 1 when it
// ignores none.
func ignoredPaths(ctx context.Context, index Git, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out, err := index.Feed(ctx, []byte(strings.Join(paths, "\x00")+"\x00"), "check-ignore", "-z", "--stdin")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ignored []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name != "" {
			ignored = append(ignored, name)
		}
	}
	return ignored, nil
}

// snapshot builds a snapshot whose index add fills from HEAD's tree.
func (w Worktrees) snapshot(ctx context.Context, path, ref string, add func(index Git) error) (string, error) {
	git := w.git(path)
	file, err := git.Text(ctx, "rev-parse", "--path-format=absolute", "--git-path", snapshotIndex)
	if err != nil {
		return "", err
	}
	for _, stale := range []string{file, file + ".lock"} {
		if err := os.Remove(stale); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	defer os.Remove(file)
	index := git
	index.Extra = snapshotIndexVariable(file)
	if _, err := index.Output(ctx, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	crashpoint.Hit("worktree.snapshot", "read")
	if err := add(index); err != nil {
		return "", err
	}
	crashpoint.Hit("worktree.snapshot", "added")
	tree, err := index.Text(ctx, "write-tree")
	if err != nil {
		return "", err
	}
	head, err := git.Commit(ctx, "HEAD")
	if err != nil {
		return "", err
	}
	previous, err := git.Commit(ctx, ref)
	hasPrevious := err == nil
	if hasPrevious {
		held, err := git.Text(ctx, "rev-parse", previous+"^{tree}")
		if err != nil {
			return "", err
		}
		if held == tree && w.isAncestor(ctx, git, head, previous) {
			return previous, nil
		}
	}
	args := []string{"commit-tree", tree, "-p", head, "-m", "ropeshift snapshot"}
	if hasPrevious {
		args = append(args, "-p", previous)
	}
	stamp := git
	stamp.Extra = snapshotIdentity
	commit, err := stamp.Text(ctx, args...)
	if err != nil {
		return "", err
	}
	crashpoint.Hit("worktree.snapshot", "committed")
	if _, err := git.Output(ctx, "update-ref", "--", ref, commit); err != nil {
		return "", err
	}
	crashpoint.Hit("worktree.snapshot", "recorded")
	return commit, nil
}

func (w Worktrees) isAncestor(ctx context.Context, git Git, ancestor, of string) bool {
	_, err := git.Output(ctx, "merge-base", "--is-ancestor", ancestor, of)
	return err == nil
}

// Push sends one ref under its own name. It runs no repository hook, because a stop must
// not depend on code in the repository it is saving, and it never forces.
func (w Worktrees) Push(ctx context.Context, path, remote, ref string) error {
	if remote == "" {
		return fmt.Errorf("no remote to push %s to", ref)
	}
	_, err := w.git(path).Output(ctx, pushArguments(remote, ref)...)
	return err
}

// PushBranch sends a branch to the remote a push of it would take, under its own name. It runs no
// repository hook and never forces, so a remote whose branch moved refuses it.
func (w Worktrees) PushBranch(ctx context.Context, repository, branch string) error {
	git := w.git(repository)
	remote, err := remoteOf(ctx, git, branch)
	if err != nil {
		return err
	}
	if remote == "" {
		return fmt.Errorf("%w: pushing %s", ports.ErrNoRemote, branch)
	}
	_, err = git.Output(ctx, pushArguments(remote, lanes.HeadsRef(branch))...)
	return err
}

// Fetch brings each ref the remote holds into this repository under its own name. A branch
// checked out here is left alone, because git refuses that, and this worktree's branch is the
// one a lane already holds. Naming a ref as its own destination also moves its remote-tracking copy,
// so a fetch can lose git's lock on that ref to intake's fetch of the clone, and it goes through
// Git.Fetch, which tries again.
func (w Worktrees) Fetch(ctx context.Context, repository string, refs []string) error {
	git := w.git(repository)
	remote, err := remoteOf(ctx, git, "")
	if err != nil || remote == "" {
		return err
	}
	for _, ref := range refs {
		listed, err := git.Text(ctx, "ls-remote", "--", remote, ref)
		if err != nil {
			return err
		}
		if listed == "" {
			continue
		}
		if err := git.Fetch(ctx, "--quiet", "--no-tags", "--", remote, ref+":"+ref); err != nil {
			return err
		}
	}
	return nil
}
