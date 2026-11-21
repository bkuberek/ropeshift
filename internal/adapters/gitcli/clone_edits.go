package gitcli

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// editsCommits bounds how many unpushed commits a read of a clone's edits walks.
const editsCommits = 1000

// cloneGit runs the reads of a person's clone. No file system monitor runs, and every command that could take the index's
// lock is given --no-optional-locks, so a read never rewrites the person's index.
func (w Worktrees) cloneGit(clone string) Git {
	git := w.git(clone)
	git.Config = append(git.Config, [2]string{"core.fsmonitor", "false"})
	return git
}

// cloneCommit reads the commit a ref names. A ref git says it does not hold is missing; every other failure, a context
// that ended among them, is an error and never read as a missing ref.
func cloneCommit(ctx context.Context, git Git, ref string) (commit string, missing bool, err error) {
	result, err := git.run(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err == nil {
		err = ctx.Err()
	}
	switch {
	case err != nil:
		return "", false, err
	case result.code == 0 && result.out != "":
		return result.out, false, nil
	case result.code == 1 && result.stderr == "":
		return "", true, nil
	}
	return "", false, fmt.Errorf("git rev-parse %s: exit %d: %s", ref, result.code, result.stderr)
}

// errEnough stops a listing once a read has kept all it keeps.
var errEnough = errors.New("the read kept all it keeps")

// Edits reads what the clone at clone holds that intake and lanes cannot read: every path `git status` lists as changed
// or untracked, and every path the checked-out branch's commits changed that no branch of the remote holds. Both
// listings are read NUL-separated, so a path is read as git holds it, and each stops once limit paths are kept. It
// fetches nothing and writes nothing, the index included. A HEAD git cannot read is an error, never a clone with no
// commit.
func (w Worktrees) Edits(ctx context.Context, clone string, limit int) (ledger.CloneEdits, error) {
	if info, err := os.Stat(clone); err != nil || !info.IsDir() {
		return ledger.CloneEdits{}, fmt.Errorf("%w: no clone at %s", ports.ErrNotFound, clone)
	}
	git := w.cloneGit(clone)
	if _, err := git.Text(ctx, "rev-parse", "--git-dir"); err != nil {
		if ctx.Err() != nil {
			return ledger.CloneEdits{}, ctx.Err()
		}
		return ledger.CloneEdits{}, fmt.Errorf("%w: %s holds no clone: %v", ports.ErrNotFound, clone, err)
	}
	out := ledger.CloneEdits{Edits: []ledger.CloneEdit{}}
	// A detached head has no symbolic name, which is an answer and not a failure.
	out.Branch, _ = git.Text(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	head, missing, err := cloneCommit(ctx, git, "HEAD")
	switch {
	case err != nil:
		return ledger.CloneEdits{}, fmt.Errorf("reading the commit the clone has checked out: %w", err)
	case missing && out.Branch == "":
		return ledger.CloneEdits{}, errors.New("the clone's HEAD names no commit and no branch")
	case missing:
		if named, _ := git.run(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/heads/"+out.Branch); named.code == 0 || ctx.Err() != nil {
			return ledger.CloneEdits{}, fmt.Errorf("the clone's branch %s names a commit git cannot read", ledger.PlainText(out.Branch))
		}
	}
	// A HEAD that names a branch with no commit yet is a clone with no commit, whose steps start a branch with no parent.
	out.Head = head
	remote, err := remoteOf(ctx, git, out.Branch)
	if err != nil {
		return ledger.CloneEdits{}, err
	}
	out.Remote = remote
	states := map[string]ledger.EditState{}
	keep := func(path string, state ledger.EditState) bool {
		if _, held := states[path]; held || path == "" {
			return true
		}
		if len(states) >= limit {
			out.Cut = true
			return false
		}
		states[path] = state
		return true
	}
	// Each entry is two status letters, a space, and the path. With --no-renames a move is a deletion and an addition,
	// so no entry carries a second path.
	status := func(entry string) bool {
		if len(entry) < 4 {
			return true
		}
		state := ledger.EditUncommitted
		if entry[:2] == "??" {
			state = ledger.EditUntracked
		}
		return keep(entry[3:], state)
	}
	if err := git.Stream(ctx, eachField(status), "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames"); err != nil && !errors.Is(err, errEnough) {
		return ledger.CloneEdits{}, err
	}
	if remote != "" && out.Head != "" && !out.Cut {
		unpushed := []string{"HEAD", "--not", "--remotes=" + remote}
		counted, err := git.Text(ctx, append([]string{"--no-optional-locks", "rev-list", "--count", "--max-count=" + strconv.Itoa(editsCommits+1)}, unpushed...)...)
		if err != nil {
			return ledger.CloneEdits{}, err
		}
		commits, err := strconv.Atoi(counted)
		if err != nil {
			return ledger.CloneEdits{}, fmt.Errorf("git rev-list --count printed %q", counted)
		}
		if commits > editsCommits {
			out.Cut = true
		}
		if commits > 0 {
			args := append([]string{"--no-optional-locks", "log", "--no-renames", "--format=", "--name-only", "-z", "--max-count=" + strconv.Itoa(editsCommits)}, unpushed...)
			if err := git.Stream(ctx, eachField(func(path string) bool { return keep(path, ledger.EditUnpushed) }), args...); err != nil && !errors.Is(err, errEnough) {
				return ledger.CloneEdits{}, err
			}
		}
	}
	for path, state := range states {
		out.Edits = append(out.Edits, ledger.CloneEdit{Path: path, State: state})
	}
	slices.SortFunc(out.Edits, func(a, b ledger.CloneEdit) int { return cmp.Compare(a.Path, b.Path) })
	return out, nil
}

// eachField reads a NUL-separated listing and hands each field to take, until take says it has kept all it keeps.
func eachField(take func(string) bool) func(io.Reader) error {
	return func(r io.Reader) error {
		fields := bufio.NewReader(r)
		for {
			field, err := fields.ReadString(0)
			if len(field) > 0 && field[len(field)-1] == 0 {
				field = field[:len(field)-1]
			}
			if field != "" && !take(field) {
				return errEnough
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
}

var _ ports.CloneEdits = Worktrees{}

// ropeshiftMerge matches the trailer of a commit a merge of ropeshift made, which is no person's change.
const ropeshiftMerge = "^" + trailerWorkOrder

// laneRef reads the commit of a branch a lane's work is on, or of the branch an edits commit is on. A name git refuses
// as a branch, or a branch the clone does not hold, is ErrNotFound; any other failure is an error.
func laneRef(ctx context.Context, git Git, ref, shown string) (string, error) {
	commit, missing, err := cloneCommit(ctx, git, ref)
	switch {
	case err != nil:
		return "", err
	case missing:
		return "", fmt.Errorf("%w: the clone holds no %s", ports.ErrNotFound, shown)
	}
	return commit, nil
}

// Diverged reads what a lane's branch and its base each changed since they last shared a commit. The base is the
// remote's copy of baseBranch as the last fetch left it, or the clone's own branch in a clone with no remote, and it is
// never fetched. The base's side counts only the commits on its first-parent line that are no merge, that carry no
// trailer of a merge of ropeshift, and that landed names as no commit of landed work, so neither the work ropeshift or
// the forge merged nor what a merge brought in reads as a person's. Each listing is read NUL-separated and stops once
// limit paths of its side are kept. Only a ref or a fork point git says is missing is ErrNotFound.
func (w Worktrees) Diverged(ctx context.Context, clone, branch, baseBranch string, landed []string, limit int) (ledger.Divergence, error) {
	for _, name := range []string{branch, baseBranch} {
		if err := ledger.CheckStoredBranch(name); err != nil {
			return ledger.Divergence{}, fmt.Errorf("%w: %v", ports.ErrNotFound, err)
		}
	}
	git := w.cloneGit(clone)
	lane := "refs/heads/" + branch
	if _, err := laneRef(ctx, git, lane, "branch "+branch); err != nil {
		return ledger.Divergence{}, err
	}
	remote, err := remoteOf(ctx, git, "")
	if err != nil {
		return ledger.Divergence{}, err
	}
	base, shown := "refs/heads/"+baseBranch, baseBranch
	if remote != "" {
		base, shown = "refs/remotes/"+remote+"/"+baseBranch, remote+"/"+baseBranch
	}
	if _, err := laneRef(ctx, git, base, shown); err != nil {
		return ledger.Divergence{}, err
	}
	shared, err := git.run(ctx, "merge-base", lane, base)
	if err == nil {
		err = ctx.Err()
	}
	switch {
	case err != nil:
		return ledger.Divergence{}, err
	case shared.code == 1:
		return ledger.Divergence{}, fmt.Errorf("%w: %s and %s share no history", ports.ErrNotFound, branch, shown)
	case shared.code != 0 || shared.out == "":
		return ledger.Divergence{}, fmt.Errorf("git merge-base %s %s: exit %d: %s", branch, shown, shared.code, shared.stderr)
	}
	fork := shared.out
	out := ledger.Divergence{Fork: fork, Base: shown, Lane: []string{}, Pushed: []string{}}
	side := func(into *[]string) func(string) bool {
		seen := map[string]bool{}
		return func(path string) bool {
			if seen[path] {
				return true
			}
			if len(*into) >= limit {
				out.Cut = true
				return false
			}
			seen[path] = true
			*into = append(*into, path)
			return true
		}
	}
	if err := git.Stream(ctx, eachField(side(&out.Lane)), "--no-optional-locks", "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", fork, lane, "--"); err != nil && !errors.Is(err, errEnough) {
		return ledger.Divergence{}, err
	}
	listed, err := git.Text(ctx, "--no-optional-locks", "log", "--first-parent", "--no-merges", "--invert-grep", "--grep="+ropeshiftMerge,
		"--format=%H", "--max-count="+strconv.Itoa(editsCommits+1), fork+".."+base, "--")
	if err != nil {
		return ledger.Divergence{}, err
	}
	commits := strings.Fields(listed)
	if len(commits) > editsCommits {
		commits, out.Cut = commits[:editsCommits], true
	}
	commits = slices.DeleteFunc(commits, func(commit string) bool { return slices.Contains(landed, commit) })
	if len(commits) > 0 {
		names, err := git.Feed(ctx, []byte(strings.Join(commits, "\n")+"\n"), "--no-optional-locks", "log", "--no-walk=unsorted", "--stdin", "--no-renames", "--format=", "--name-only", "-z")
		if err != nil {
			return ledger.Divergence{}, err
		}
		if err := eachField(side(&out.Pushed))(bytes.NewReader(names)); err != nil && !errors.Is(err, errEnough) {
			return ledger.Divergence{}, err
		}
	}
	slices.Sort(out.Lane)
	slices.Sort(out.Pushed)
	return out, nil
}

// Contents reads the blob each path's file in the clone would be stored as, with no filter run and no object written,
// and empty for a path whose file is gone.
func (w Worktrees) Contents(ctx context.Context, clone string, paths []string) (map[string]string, error) {
	out := make(map[string]string, len(paths))
	var present []string
	for _, path := range paths {
		out[path] = ""
		if _, err := os.Lstat(filepath.Join(clone, filepath.FromSlash(path))); err == nil {
			present = append(present, path)
		}
	}
	if len(present) == 0 {
		return out, nil
	}
	hashed, err := w.cloneGit(clone).Feed(ctx, []byte(strings.Join(present, "\n")+"\n"), "hash-object", "--no-filters", "--stdin-paths")
	if err != nil {
		return nil, err
	}
	blobs := strings.Fields(string(hashed))
	if len(blobs) != len(present) {
		return nil, fmt.Errorf("git hash-object printed %d blobs for %d files", len(blobs), len(present))
	}
	for i, path := range present {
		out[path] = blobs[i]
	}
	return out, nil
}

// Holds says a lane's branch holds the branch an edits commit is on. An edits branch nobody made is held by no branch;
// a lane's branch the clone does not hold is ErrNotFound.
func (w Worktrees) Holds(ctx context.Context, clone, branch, ancestor string) (bool, error) {
	for _, name := range []string{branch, ancestor} {
		if err := ledger.CheckStoredBranch(name); err != nil {
			return false, fmt.Errorf("%w: %v", ports.ErrNotFound, err)
		}
	}
	git := w.cloneGit(clone)
	if _, err := laneRef(ctx, git, "refs/heads/"+branch, "branch "+branch); err != nil {
		return false, err
	}
	switch _, missing, err := cloneCommit(ctx, git, "refs/heads/"+ancestor); {
	case err != nil:
		return false, err
	case missing:
		return false, nil
	}
	held, err := git.run(ctx, "merge-base", "--is-ancestor", "refs/heads/"+ancestor, "refs/heads/"+branch)
	if err == nil {
		err = ctx.Err()
	}
	switch {
	case err != nil:
		return false, err
	case held.code == 0:
		return true, nil
	case held.code == 1:
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor: exit %d: %s", held.code, held.stderr)
}
