package gitcli

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"

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
