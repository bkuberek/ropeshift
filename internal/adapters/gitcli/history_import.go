package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ports"
)

// pathsLiterally has git read every path it is given as the path and never as a pattern, because a file of a
// design repository may be named with a character a pattern means.
var pathsLiterally = []string{"GIT_LITERAL_PATHSPECS=1"}

// pathBatch is how many paths one command is given, so a repository with many files stays under the limit on
// the length of a command line.
const pathBatch = 200

// ignoreFile is the one path both histories may hold that a merge unites and does not keep the branch's version of,
// in whatever case either history spells it.
const ignoreFile = ".gitignore"

// foldPath is a path with its case removed: two paths that fold alike are one path.
func foldPath(name string) string { return strings.ToLower(name) }

// caseVariants lists the paths the history holds under another case than the branch holds the same path, each with
// the branch's spelling.
func caseVariants(ours, theirs map[string]treeEntry) map[string]string {
	held := make(map[string]string, len(ours))
	for name := range ours {
		held[foldPath(name)] = name
	}
	variants := map[string]string{}
	for name := range theirs {
		if mine, found := held[foldPath(name)]; found && mine != name {
			variants[name] = mine
		}
	}
	return variants
}

// foldedEntry is the file of a tree whose path folds to the ignore file's, under the name the tree spells it.
func foldedEntry(held map[string]treeEntry) (string, treeEntry, bool) {
	for name, entry := range held {
		if foldPath(name) == ignoreFile {
			return name, entry, true
		}
	}
	return "", treeEntry{}, false
}

// ImportHistory merges the history of another repository into a worktree's branch (FR-ADOPT-13). It reads the
// source with `git fetch`, which writes nothing to it, and merges as `git merge --allow-unrelated-histories
// --no-ff --no-commit` does, so no commit is rewritten and the branch is the first parent.
//
// A crash leaves one of three things, and a retry reads which. The commit is on the branch, and the retry finds
// the history there and does nothing. The merge was begun and no commit made, and the retry throws away what the
// merge and the files left in the worktree and makes the merge again, which only this effect has written. Or
// nothing was written, and the retry runs from the start.
func (w Worktrees) ImportHistory(ctx context.Context, worktree string, in ports.HistoryImport) (ports.Commit, error) {
	if _, err := os.Stat(filepath.Join(worktree, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ports.Commit{}, fmt.Errorf("worktree %s: %w", worktree, ports.ErrNotFound)
		}
		return ports.Commit{}, err
	}
	if err := checkImport(in); err != nil {
		return ports.Commit{}, err
	}
	git := w.git(worktree)
	git.Extra = slices.Concat(commitEnvironment, pathsLiterally)
	if err := git.Fetch(ctx, "--quiet", "--no-tags", "--", in.Source, in.Commit); err != nil {
		return ports.Commit{}, fmt.Errorf("reading the history of %s: %w", in.Source, err)
	}
	tip, err := git.Commit(ctx, in.Commit)
	if err != nil {
		return ports.Commit{}, fmt.Errorf("the fetch from %s brought no commit %s: %w", in.Source, in.Commit, err)
	}
	if held, err := isAncestor(ctx, git, tip, "HEAD"); err != nil || held {
		return ports.Commit{}, err
	}
	root, err := git.Text(ctx, "rev-list", "--first-parent", "--max-parents=0", tip)
	if err != nil {
		return ports.Commit{}, err
	}
	if root != in.Root {
		return ports.Commit{}, &ports.HistoryRootError{Want: in.Root, Got: root}
	}
	if err := abandonMerge(ctx, git, false); err != nil {
		return ports.Commit{}, err
	}
	made, err := w.mergeHistory(ctx, git, tip, in)
	if err != nil {
		// The merge may be half made, and a worktree left so would fail the lane with files nobody chose.
		return ports.Commit{}, errors.Join(err, abandonMerge(context.WithoutCancel(ctx), git, true))
	}
	return made, nil
}

// checkImport refuses a request that would hand git something other than a commit ID, a clean path, or a file
// the worktree could hold.
func checkImport(in ports.HistoryImport) error {
	if in.Source == "" || strings.HasPrefix(in.Source, "-") {
		return fmt.Errorf("%q is no clone to import a history from", in.Source)
	}
	if len(in.Commit) < 40 || strings.Trim(in.Commit, "0123456789abcdef") != "" {
		return fmt.Errorf("%q is no commit ID", in.Commit)
	}
	for _, tree := range in.LeaveOut {
		if err := checkTreePath(tree); err != nil {
			return err
		}
	}
	for _, file := range in.Files {
		if err := checkTreePath(file.Path); err != nil {
			return err
		}
	}
	return nil
}

// isAncestor says a commit is reachable from another, which `git merge-base --is-ancestor` answers with its exit
// status.
func isAncestor(ctx context.Context, git Git, commit, of string) (bool, error) {
	result, err := git.run(ctx, "merge-base", "--is-ancestor", commit, of)
	if err != nil {
		return false, err
	}
	switch result.code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor: exit %d: %s", result.code, result.stderr)
}

// merging says a merge is in progress in the worktree.
func merging(ctx context.Context, git Git) (bool, error) {
	result, err := git.run(ctx, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	if err != nil {
		return false, err
	}
	return result.code == 0, nil
}

// abandonMerge puts the worktree back as the branch's head has it when a merge was begun and not finished, and
// leaves a worktree with no merge in progress alone unless always is true. It removes the untracked files too,
// because the files a merge brought and the files this effect wrote are untracked until the commit.
func abandonMerge(ctx context.Context, git Git, always bool) error {
	begun, err := merging(ctx, git)
	if err != nil || !begun && !always {
		return err
	}
	if _, err := git.Output(ctx, "reset", "--hard", "--quiet", "HEAD"); err != nil {
		return err
	}
	_, err = git.Output(ctx, "clean", "-fdq")
	return err
}

// treeEntry is one file of a commit: what git calls its mode and its object, which tell whether two commits hold
// the same file at a path.
type treeEntry struct{ mode, object string }

// entries lists the files of a commit by path.
func entries(ctx context.Context, git Git, commit string) (map[string]treeEntry, error) {
	out, err := git.Output(ctx, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return nil, err
	}
	held := map[string]treeEntry{}
	for line := range strings.SplitSeq(string(out), "\x00") {
		meta, name, found := strings.Cut(line, "\t")
		fields := strings.Fields(meta)
		if found && len(fields) == 3 {
			held[name] = treeEntry{mode: fields[0], object: fields[2]}
		}
	}
	return held, nil
}

// mergeHistory makes the merge: the history in, the paths both hold settled, the trees left out removed, the files
// added, and one commit.
func (w Worktrees) mergeHistory(ctx context.Context, git Git, tip string, in ports.HistoryImport) (ports.Commit, error) {
	ours, err := entries(ctx, git, "HEAD")
	if err != nil {
		return ports.Commit{}, err
	}
	theirs, err := entries(ctx, git, tip)
	if err != nil {
		return ports.Commit{}, err
	}
	// A clean merge and one that conflicts both leave the merge in progress, so what separates them from a merge
	// git refused is that one.
	merged, err := git.run(ctx, "merge", "--allow-unrelated-histories", "--no-ff", "--no-commit", "--quiet", tip)
	if err != nil {
		return ports.Commit{}, err
	}
	begun, err := merging(ctx, git)
	if err != nil {
		return ports.Commit{}, err
	}
	if !begun {
		return ports.Commit{}, fmt.Errorf("git merge %s made no merge: exit %d: %s %s", tip, merged.code, merged.out, merged.stderr)
	}
	if err := settleCollisions(ctx, git, ours, theirs); err != nil {
		return ports.Commit{}, err
	}
	if err := removeLeftOut(ctx, git, ours, theirs, in.LeaveOut); err != nil {
		return ports.Commit{}, err
	}
	var staged []string
	for _, file := range in.Files {
		stage, err := place(git.Dir, file)
		if err != nil {
			return ports.Commit{}, err
		}
		if stage {
			staged = append(staged, file.Path)
		}
	}
	if err := inBatches(staged, func(batch []string) error {
		_, err := git.Output(ctx, append([]string{"add", "--"}, batch...)...)
		return err
	}); err != nil {
		return ports.Commit{}, err
	}
	if unmerged, err := git.Output(ctx, "ls-files", "--unmerged", "-z"); err != nil || len(unmerged) != 0 {
		return ports.Commit{}, errors.Join(err, unresolved(unmerged))
	}
	if _, err := git.Output(ctx, "commit", "--quiet", "--no-verify", "-m", in.Subject); err != nil {
		return ports.Commit{}, err
	}
	return describeMerge(ctx, git, in.Subject)
}

// unresolved says which paths the merge left in conflict that settling the paths both histories hold did not
// reach, which is a path that is a file in one history and a folder in the other.
func unresolved(unmerged []byte) error {
	if len(unmerged) == 0 {
		return nil
	}
	var names []string
	for line := range strings.SplitSeq(string(unmerged), "\x00") {
		if _, name, found := strings.Cut(line, "\t"); found && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return fmt.Errorf("the merge leaves %s in conflict, because each history holds it as another kind of entry, so a person has to settle it", strings.Join(names, ", "))
}

// inBatches runs a command over the paths a few at a time.
func inBatches(paths []string, run func(batch []string) error) error {
	for batch := range slices.Chunk(paths, pathBatch) {
		if err := run(batch); err != nil {
			return err
		}
	}
	return nil
}

// settleCollisions keeps the branch's version of every path both histories hold with different content, except the
// ignore file, which becomes the branch's lines followed by each of the history's lines the branch lacks, in order.
// A path the history holds under another case than the branch is the branch's path: the history's spelling is
// dropped from the merge, and the branch's file is written back, because on a file system that ignores case the
// merge wrote the history's text over it.
func settleCollisions(ctx context.Context, git Git, ours, theirs map[string]treeEntry) error {
	var kept, dropped []string
	for name, held := range ours {
		if other, both := theirs[name]; both && other != held && foldPath(name) != ignoreFile {
			kept = append(kept, name)
		}
	}
	for variant, name := range caseVariants(ours, theirs) {
		dropped = append(dropped, variant)
		kept = append(kept, name)
	}
	slices.Sort(dropped)
	slices.Sort(kept)
	kept = slices.Compact(kept)
	if err := inBatches(dropped, func(batch []string) error {
		_, err := git.Output(ctx, append([]string{"rm", "--quiet", "--force", "--"}, batch...)...)
		return err
	}); err != nil {
		return err
	}
	if err := inBatches(kept, func(batch []string) error {
		_, err := git.Output(ctx, append([]string{"checkout", "--quiet", "HEAD", "--"}, batch...)...)
		return err
	}); err != nil {
		return err
	}
	at, mine, both := foldedEntry(ours)
	_, other, there := foldedEntry(theirs)
	if !both || !there || mine == other {
		return nil
	}
	first, err := git.Output(ctx, "cat-file", "blob", mine.object)
	if err != nil {
		return err
	}
	second, err := git.Output(ctx, "cat-file", "blob", other.object)
	if err != nil {
		return err
	}
	text, _ := withLines(string(first), strings.Split(strings.TrimSuffix(string(second), "\n"), "\n"))
	if err := refuseLinks(git.Dir, at); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(git.Dir, at), []byte(text)); err != nil {
		return err
	}
	_, err = git.Output(ctx, "add", "--", at)
	return err
}

// removeLeftOut removes from the merge what the history holds inside each tree left out, and no path the branch
// held, in any case, so a tree left out never takes a file of the workspace with it.
func removeLeftOut(ctx context.Context, git Git, ours, theirs map[string]treeEntry, trees []string) error {
	variants := caseVariants(ours, theirs)
	var removed []string
	for name := range theirs {
		if _, held := ours[name]; held {
			continue
		}
		if _, variant := variants[name]; variant {
			continue
		}
		if slices.ContainsFunc(trees, func(tree string) bool { return name == tree || strings.HasPrefix(name, path.Clean(tree)+"/") }) {
			removed = append(removed, name)
		}
	}
	slices.Sort(removed)
	return inBatches(removed, func(batch []string) error {
		_, err := git.Output(ctx, append([]string{"rm", "--quiet", "--force", "--"}, batch...)...)
		return err
	})
}

// describeMerge reads the commit the merge made: its parents, first parent first, and the files it changed
// against the first one.
func describeMerge(ctx context.Context, git Git, subject string) (ports.Commit, error) {
	hash, err := git.Text(ctx, "rev-parse", "HEAD")
	if err != nil {
		return ports.Commit{}, err
	}
	parents, err := git.Text(ctx, "rev-list", "--parents", "-n", "1", hash)
	if err != nil {
		return ports.Commit{}, err
	}
	listing, err := git.Output(ctx, "diff", "--name-only", "-z", hash+"^1", hash)
	if err != nil {
		return ports.Commit{}, err
	}
	return ports.Commit{Hash: hash, Subject: subject, Files: splitNames(listing), Parents: strings.Fields(parents)[1:]}, nil
}

var _ ports.HistoryImports = Worktrees{}
