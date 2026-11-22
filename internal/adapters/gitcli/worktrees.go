package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Worktrees manages git worktrees with the git command line. Env replaces the
// environment of every command, which is how a test keeps its own configuration.
type Worktrees struct {
	Env []string
}

func (w Worktrees) git(dir string) Git { return Git{Dir: dir, Env: w.Env} }

// listed is one entry of `git worktree list --porcelain`. A detached head has no branch, and says so. A
// prunable entry is one `git worktree prune` would remove.
type listed struct {
	path     string
	branch   string
	detached bool
	prunable bool
}

// list reads every worktree the repository registers. It is the middle step of a create, between the prune
// and the add, and the check after the add, and it reads every registration, so it holds the repository's
// worktree lock like they do.
func (w Worktrees) list(ctx context.Context, repository string) ([]listed, error) {
	git := w.git(repository)
	release, err := git.holdWorktrees(ctx)
	if err != nil {
		return nil, err
	}
	out, err := git.Text(ctx, "worktree", "list", "--porcelain")
	release()
	if err != nil {
		return nil, err
	}
	var entries []listed
	for block := range strings.SplitSeq(out, "\n\n") {
		var entry listed
		for line := range strings.SplitSeq(block, "\n") {
			if path, ok := strings.CutPrefix(line, "worktree "); ok {
				entry.path = path
			}
			if branch, ok := strings.CutPrefix(line, "branch refs/heads/"); ok {
				entry.branch = branch
			}
			if line == "detached" {
				entry.detached = true
			}
			if strings.HasPrefix(line, "prunable") {
				entry.prunable = true
			}
		}
		if entry.path != "" {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// CheckedOut names the linked worktree of the repository that has branch checked out. It reads the registrations
// under the repository's worktree lock, skips the first, which is the repository's own working tree, and skips a
// registration whose directory is gone, which the next create prunes.
func (w Worktrees) CheckedOut(ctx context.Context, repository, branch string) (string, error) {
	entries, err := w.list(ctx, repository)
	if err != nil {
		return "", err
	}
	for i, entry := range entries {
		if i == 0 || entry.detached || entry.branch != branch {
			continue
		}
		if info, err := os.Stat(entry.path); err != nil || !info.IsDir() {
			continue
		}
		return entry.path, nil
	}
	return "", nil
}

var _ ports.BranchTrees = Worktrees{}

// canonical resolves symlinks in the part of a path that exists, because git reports
// a worktree's real path and a temporary directory is often behind a link.
func canonical(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(canonical(parent), filepath.Base(path))
}

// detachedMarker is the file that says a worktree was made detached. It sits in the worktree's own git
// directory, so it goes with the worktree and no other worktree reads it.
const detachedMarker = "ropeshift-detached"

// markerPath resolves the marker's place for the worktree a command stands in.
func markerPath(ctx context.Context, git Git) (string, error) {
	return git.Text(ctx, "rev-parse", "--path-format=absolute", "--git-path", detachedMarker)
}

// markDetached records that a worktree was made detached. It repeats safely, so a retry after a crash
// between the checkout and this converges.
func (w Worktrees) markDetached(ctx context.Context, path string) error {
	file, err := markerPath(ctx, w.git(path))
	if err != nil {
		return err
	}
	return os.WriteFile(file, nil, 0o600)
}

// madeDetached reads the marker.
func madeDetached(ctx context.Context, git Git) bool {
	file, err := markerPath(ctx, git)
	if err != nil {
		return false
	}
	_, err = os.Stat(file)
	return err == nil
}

// Create makes a worktree. It prunes first, so a worktree whose directory went away
// in a crash does not hold its branch, and an existing match is the answer to a
// retry. A detached worktree is the commit checked out and no branch, so it can neither take a
// branch's name nor leave one behind.
//
// A registration that a prune in another process removes while the add is making it fails the add, or
// leaves the add a success and the registration gone or without its gitdir. A create checks the
// registration after its add, and on either does the whole step again, because the prune, the list, and the
// add each read what the others left.
func (w Worktrees) Create(ctx context.Context, spec ports.WorktreeSpec) (ports.Worktree, error) {
	var tree ports.Worktree
	err := retryLostRegistration(ctx, func() (err error) {
		tree, err = w.create(ctx, spec)
		return err
	})
	return tree, err
}

// create is one run of a create.
//
// Every git command of it runs no repository hook: the fetch and the add change refs and check a tree out, and a
// hook a lane wrote would run outside the lane's gates.
func (w Worktrees) create(ctx context.Context, spec ports.WorktreeSpec) (ports.Worktree, error) {
	repository, err := w.hookless(ctx, spec.Repository)
	if err != nil {
		return ports.Worktree{}, err
	}
	if prunesBeforeCreate() {
		if err := repository.changeWorktrees(ctx, "prune"); err != nil {
			return ports.Worktree{}, err
		}
	}
	crashpoint.Hit("worktree.create", "pruned")
	entries, err := w.list(ctx, spec.Repository)
	if err != nil {
		return ports.Worktree{}, err
	}
	want := canonical(spec.Path)
	for _, entry := range entries {
		switch {
		case entry.path == want && entry.detached != spec.Detached:
			return ports.Worktree{}, fmt.Errorf("%w: %s holds %s", ports.ErrWorktreeConflict, spec.Path, heldBy(entry))
		case entry.path == want && entry.branch == spec.Branch && !spec.Detached:
			return w.Inspect(ctx, spec.Path)
		case entry.path == want && spec.Detached:
			if err := w.markDetached(ctx, spec.Path); err != nil {
				return ports.Worktree{}, err
			}
			return w.Inspect(ctx, spec.Path)
		case entry.path == want:
			return ports.Worktree{}, fmt.Errorf("%w: %s holds %s", ports.ErrWorktreeConflict, spec.Path, entry.branch)
		case !spec.Detached && entry.branch == spec.Branch:
			return ports.Worktree{}, fmt.Errorf("%w: %s is checked out at %s", ports.ErrWorktreeConflict, spec.Branch, entry.path)
		}
	}
	if err := refuseOccupied(spec.Path); err != nil {
		return ports.Worktree{}, err
	}
	if err := os.MkdirAll(filepath.Dir(spec.Path), 0o700); err != nil {
		return ports.Worktree{}, err
	}
	base := spec.Base
	if base == "" {
		base = "HEAD"
	}
	if strings.HasPrefix(base, "-") {
		return ports.Worktree{}, fmt.Errorf("%w: %s", ports.ErrNotFound, base)
	}
	args := []string{"add", "--quiet"}
	switch {
	case spec.Detached:
		if err := w.fetchMissing(ctx, repository, base); err != nil {
			return ports.Worktree{}, err
		}
		args = append(args, "--detach", "--", spec.Path, base)
	default:
		if _, err := repository.Commit(ctx, "refs/heads/"+spec.Branch); err == nil {
			args = append(args, "--", spec.Path, spec.Branch)
		} else {
			args = append(args, "-b", spec.Branch, "--", spec.Path, base)
		}
	}
	if err := repository.changeWorktrees(ctx, args...); err != nil {
		return ports.Worktree{}, err
	}
	crashpoint.Hit("worktree.create", "added")
	tree, err := w.finish(ctx, spec)
	registered, listErr := w.registered(ctx, spec.Repository, spec.Path)
	if listErr != nil {
		return ports.Worktree{}, listErr
	}
	if !registered || lostRegistration(err) {
		// The path held nothing before this add, so what is there now is the add's checkout and no one's work.
		if err := os.RemoveAll(spec.Path); err != nil {
			return ports.Worktree{}, err
		}
		return ports.Worktree{}, fmt.Errorf("%w: %s", errRegistrationLost, spec.Path)
	}
	return tree, err
}

// finish completes a create after its add: it marks a detached worktree, and reads it.
func (w Worktrees) finish(ctx context.Context, spec ports.WorktreeSpec) (ports.Worktree, error) {
	if spec.Detached {
		if err := w.markDetached(ctx, spec.Path); err != nil {
			return ports.Worktree{}, err
		}
	}
	return w.Inspect(ctx, spec.Path)
}

// registered reports whether the repository registers a worktree at path that no prune removes: `git worktree
// list` names it, and does not call it prunable. A prune that removed a registration file by file while the
// add was writing it can leave the add a success and the registration without its gitdir, which git then
// neither lists nor keeps.
func (w Worktrees) registered(ctx context.Context, repository, path string) (bool, error) {
	entries, err := w.list(ctx, repository)
	if err != nil {
		return false, err
	}
	want := canonical(path)
	for _, entry := range entries {
		if entry.path == want {
			return !entry.prunable, nil
		}
	}
	return false, nil
}

// heldBy says what a path's worktree stands on, for a refusal.
func heldBy(entry listed) string {
	if entry.detached {
		return "a detached head"
	}
	return entry.branch
}

// refuseOccupied leaves a path that holds someone's files alone. An empty directory
// is what a crash before the checkout leaves, and git takes it.
func refuseOccupied(path string) error {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%w: %s holds files", ports.ErrWorktreeConflict, path)
	}
	return nil
}

// Inspect reads a worktree.
func (w Worktrees) Inspect(ctx context.Context, path string) (ports.Worktree, error) {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return ports.Worktree{}, fmt.Errorf("worktree %s: %w", path, ports.ErrNotFound)
	}
	git := w.git(path)
	head, err := git.Text(ctx, "rev-parse", "HEAD")
	if err != nil {
		return ports.Worktree{}, err
	}
	// A detached head has no symbolic name, which is an answer and not a failure.
	branch, _ := git.Text(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	status, err := readStatus(ctx, git)
	if err != nil {
		return ports.Worktree{}, err
	}
	return ports.Worktree{Path: path, Branch: branch, Head: head, Clean: status == "", Detached: madeDetached(ctx, git)}, nil
}

// readStatus reads what differs from HEAD. Status refreshes the index when it can, which a
// reader must not do to a worktree a session is writing in.
func readStatus(ctx context.Context, git Git) (string, error) {
	return git.Text(ctx, "--no-optional-locks", "status", "--porcelain")
}

// Remove deletes a clean worktree. Git refuses a dirty one on its own, and this
// checks first so the refusal is a typed answer. The branch stays, because a lane's
// work outlives its directory.
func (w Worktrees) Remove(ctx context.Context, repository, path string) error {
	current, err := w.Inspect(ctx, path)
	if errors.Is(err, ports.ErrNotFound) {
		return w.git(repository).changeWorktrees(ctx, "prune")
	}
	if err != nil {
		return err
	}
	if !current.Clean {
		return fmt.Errorf("%w: %s", ports.ErrWorktreeDirty, path)
	}
	return w.git(repository).changeWorktrees(ctx, "remove", "--", path)
}

// Discard deletes a worktree whatever it holds, and keeps its branch. Only a caller that
// has saved the work, or a member who said to lose it, gets here.
func (w Worktrees) Discard(ctx context.Context, repository, path string) error {
	if _, err := os.Stat(filepath.Join(path, ".git")); errors.Is(err, os.ErrNotExist) {
		return w.git(repository).changeWorktrees(ctx, "prune")
	}
	return w.git(repository).changeWorktrees(ctx, "remove", "--force", "--", path)
}

// DefaultBranch reads a remote's HEAD where it has one, and a checkout's HEAD otherwise.
func (w Worktrees) DefaultBranch(ctx context.Context, repository string) (string, error) {
	git := w.git(repository)
	if head, err := git.Text(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && head != "" {
		return strings.TrimPrefix(head, "origin/"), nil
	}
	return git.Text(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
}

// contextIgnore is what keeps a pack's directory out of git. It lives in that directory, so it
// needs no entry in a file a repository or its other worktrees share.
const contextIgnore = "*\n"

// WriteContext leaves a pack's text where a session reads it. A rename means a session never reads
// half a pack, and the directory ignores itself, so git sees nothing there.
func (w Worktrees) WriteContext(_ context.Context, path, text string) (string, error) {
	return writeContext(path, filepath.Base(lanes.PackFile), text)
}

// WriteContextFile leaves one more file in the pack's directory, with the same rules. The name is a bare
// file name that does not start with a dot, so no write can leave the directory or replace the file that
// keeps it out of git.
func (w Worktrees) WriteContextFile(_ context.Context, path, name, text string) (string, error) {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("a context file is named by a bare file name, not %q", name)
	}
	return writeContext(path, name, text)
}

func writeContext(path, name, text string) (string, error) {
	dir := filepath.Join(path, filepath.Dir(lanes.PackFile))
	file := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(contextIgnore), 0o600); err != nil {
		return "", err
	}
	scratch, err := os.CreateTemp(dir, name+".*")
	if err != nil {
		return "", err
	}
	defer os.Remove(scratch.Name())
	if _, err := scratch.WriteString(text); err != nil {
		_ = scratch.Close()
		return "", err
	}
	if err := scratch.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(scratch.Name(), file); err != nil {
		return "", err
	}
	return file, nil
}

// Diff reads what head changed since base, as the repository's own history says. It runs no external
// diff program and no text conversion a repository's attributes name, because a worker reading a lane's
// work must not run the lane's code, and it names its prefixes so a user's settings do not change what a
// reviewer reads.
func (w Worktrees) Diff(ctx context.Context, repository, base, head string) (ports.Diff, error) {
	git := w.git(repository)
	for _, name := range []string{base, head} {
		if strings.HasPrefix(name, "-") {
			return ports.Diff{}, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
		if _, err := git.Commit(ctx, name); err != nil {
			return ports.Diff{}, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
	}
	options := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "-M", "--src-prefix=a/", "--dst-prefix=b/"}
	patch, err := git.Output(ctx, append(slices.Clone(options), base, head, "--")...)
	if err != nil {
		return ports.Diff{}, err
	}
	listing, err := git.Output(ctx, "diff", "--name-only", "-z", "--no-ext-diff", "-M", base, head, "--")
	if err != nil {
		return ports.Diff{}, err
	}
	var files []string
	for name := range strings.SplitSeq(string(listing), "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	return ports.Diff{Patch: string(patch), Files: files}, nil
}

// ChangeSize counts what head changed since base with `git diff --numstat`, detecting renames as Diff does,
// so a moved file is one file (FR-OBS-13). Like Diff it runs no external diff program and no text
// conversion a repository's attributes name.
func (w Worktrees) ChangeSize(ctx context.Context, path, base, head string) (ledger.ChangeSize, error) {
	git := w.git(path)
	for _, name := range []string{base, head} {
		if strings.HasPrefix(name, "-") {
			return ledger.ChangeSize{}, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
		if _, err := git.Commit(ctx, name); err != nil {
			return ledger.ChangeSize{}, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
		}
	}
	numstat, err := git.Output(ctx, "diff", "--numstat", "--no-color", "--no-ext-diff", "--no-textconv", "-M", base, head, "--")
	if err != nil {
		return ledger.ChangeSize{}, err
	}
	return lanes.ChangeSizeOf(string(numstat))
}

var (
	_ ports.Worktrees   = Worktrees{}
	_ ports.ReviewTrees = Worktrees{}
	_ ports.ChangeSizes = Worktrees{}
)

// fetchMissing brings a commit this repository does not hold from its remote, which a review needs when the
// head it reads was pushed from another clone. It fetches the remote's branches into their remote-tracking
// refs, so no branch of the checkout moves. A commit the repository holds, or a repository with no remote,
// needs nothing.
func (w Worktrees) fetchMissing(ctx context.Context, repository Git, commit string) error {
	if _, err := repository.Commit(ctx, commit); err == nil {
		return nil
	}
	remote, err := remoteOf(ctx, repository, "")
	if err != nil || remote == "" {
		return err
	}
	return repository.Fetch(ctx, "--quiet", "--no-tags", remote)
}

// FetchCommit brings a commit this repository does not hold from its remote, as a detached worktree's creation
// does.
func (w Worktrees) FetchCommit(ctx context.Context, repository, commit string) error {
	if strings.HasPrefix(commit, "-") {
		return fmt.Errorf("%w: %s", ports.ErrNotFound, commit)
	}
	return w.fetchMissing(ctx, w.git(repository), commit)
}

// hookless is the git of a repository's commands that run no repository hook, however git is asked to find
// one. core.hooksPath points at nothing, and since git 2.54 a hook named in the configuration with `hook.<name>.command`
// runs wherever core.hooksPath points, so each such name is disabled with `hook.<name>.enabled`
// (https://git-scm.com/docs/git-hook).
func (w Worktrees) hookless(ctx context.Context, dir string) (Git, error) {
	git := w.git(dir)
	names, err := git.Output(ctx, "config", "--name-only", "--get-regexp", `^hook\..*\.(command|event)$`)
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return Git{}, err
	}
	git.Config = NoHooks()
	seen := map[string]bool{}
	for line := range strings.Lines(string(names)) {
		key := strings.TrimSpace(line)
		name := strings.TrimPrefix(key, "hook.")
		if dot := strings.LastIndex(name, "."); dot > 0 && name != key {
			name = name[:dot]
			if !seen[name] {
				seen[name] = true
				git.Config = append(git.Config, [2]string{"hook." + name + ".enabled", "false"})
			}
		}
	}
	return git, nil
}
