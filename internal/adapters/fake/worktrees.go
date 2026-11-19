package fake

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// ErrPushRefused stands for a remote that takes nothing.
var ErrPushRefused = errors.New("the remote refused the push")

// Worktrees keeps worktrees in memory, so the core runs with no git.
type Worktrees struct {
	mu        sync.Mutex
	byPath    map[string]ports.Worktree
	branches  map[string]string
	remote    string
	ahead     map[string]int64
	refuse    bool
	survey    error
	pushed    []string
	fetched   []string
	snapshots map[string]string
	contexts  map[string][]string
	files     map[string]map[string]string
	touched   map[string][]string
	context   error

	// fetchedCommits is every commit FetchCommit asked for.
	fetchedCommits []string

	// history is the commits a repository holds, each with the commits it grew from. tips maps a
	// repository and a branch to its commit. specs lists every worktree asked for, in order.
	history   map[string][]string
	tips      map[string]string
	fetchFail error
	specs     []ports.WorktreeSpec
	grown     int
	// merges is every branch merge asked for, and mergeAs what each comes to while it is set.
	merges  []ports.BranchMerge
	mergeAs *ledger.MergeResult
	// held is the files each worktree holds by CommitFiles or SeedFile, fileCommits the commits CommitFiles
	// made in each, and commitFail what makes it fail.
	held        map[string]map[string]string
	fileCommits map[string][]ports.Commit
	commitFail  error
	// imports are the history imports made, and importFail what makes ImportHistory fail.
	imports    []ports.HistoryImport
	importFail error
	// subjects is the subject of each commit that has one, and trees the paths each commit holds, which
	// BranchCommits and CommitPaths read.
	subjects map[string]string
	trees    map[string][]string
}

// NewWorktrees returns an empty store.
func NewWorktrees() *Worktrees {
	return &Worktrees{
		byPath: map[string]ports.Worktree{}, branches: map[string]string{}, ahead: map[string]int64{},
		snapshots: map[string]string{}, contexts: map[string][]string{}, files: map[string]map[string]string{}, touched: map[string][]string{},
		history: map[string][]string{}, tips: map[string]string{}, fileCommits: map[string][]ports.Commit{},
	}
}

// FailContext makes every WriteContext fail, until it is given nil.
func (w *Worktrees) FailContext(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.context = err
}

// Context reads the last pack text left in a worktree.
func (w *Worktrees) Context(path string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	writes := w.contexts[path]
	if len(writes) == 0 {
		return ""
	}
	return writes[len(writes)-1]
}

// ContextWrites lists every pack text left in a worktree, oldest first.
func (w *Worktrees) ContextWrites(path string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.contexts[path])
}

// WriteContext keeps the text, and git sees none of it, so a survey reads no change.
func (w *Worktrees) WriteContext(_ context.Context, path, text string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.context != nil {
		return "", w.context
	}
	w.contexts[path] = append(w.contexts[path], text)
	return filepath.Join(path, lanes.PackFile), nil
}

// WriteContextFile keeps the text of a named file beside the pack, as git would see none of it.
func (w *Worktrees) WriteContextFile(_ context.Context, path, name, text string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("a context file is named by a bare file name, not %q", name)
	}
	if w.context != nil {
		return "", w.context
	}
	if w.files[path] == nil {
		w.files[path] = map[string]string{}
	}
	w.files[path][name] = text
	return filepath.Join(path, filepath.Dir(lanes.PackFile), name), nil
}

// ContextFile reads the last text left in a worktree under a name, and whether any was.
func (w *Worktrees) ContextFile(path, name string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	text, ok := w.files[path][name]
	return text, ok
}

// UseRemote gives every worktree a remote.
func (w *Worktrees) UseRemote(name string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.remote = name
}

// RefusePushes makes every push fail, until it is given false.
func (w *Worktrees) RefusePushes(on bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refuse = on
}

// FailSurvey makes every Survey an incomplete read, until it is given nil.
func (w *Worktrees) FailSurvey(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.survey = err
}

// Commit stands for a commit only this branch holds.
func (w *Worktrees) Commit(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ahead[path]++
}

// Fetch keeps which refs a repository asked for.
func (w *Worktrees) Fetch(_ context.Context, _ string, refs []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fetched = append(w.fetched, refs...)
	return nil
}

// FetchCommit keeps which commits a repository asked a remote for.
func (w *Worktrees) FetchCommit(_ context.Context, _, commit string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fetchedCommits = append(w.fetchedCommits, commit)
	return nil
}

// FetchedCommits lists every commit a fetch asked for, in order.
func (w *Worktrees) FetchedCommits() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string{}, w.fetchedCommits...)
}

// Fetched lists every ref a fetch asked for, in order.
func (w *Worktrees) Fetched() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string{}, w.fetched...)
}

// Pushed lists every ref a push took, in order.
func (w *Worktrees) Pushed() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string{}, w.pushed...)
}

// Snapshots maps each ref to the worktree it was taken from.
func (w *Worktrees) Snapshots() map[string]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]string, len(w.snapshots))
	for ref, path := range w.snapshots {
		out[ref] = path
	}
	return out
}

// MoveHead stands for a commit made in a worktree: its head is now that commit.
func (w *Worktrees) MoveHead(path, commit string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entry := w.byPath[path]
	entry.Head = commit
	w.byPath[path] = entry
}

// Dirty stands for an edit in a worktree.
func (w *Worktrees) Dirty(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entry := w.byPath[path]
	entry.Clean = false
	w.byPath[path] = entry
}

// LaneHead is the commit a worktree made from no base stands at: a valid commit ID, as a real worktree has.
const LaneHead = "fa4e0000000000000000000000000000000000ad"

// Create makes a worktree, or returns the one that matches.
func (w *Worktrees) Create(_ context.Context, spec ports.WorktreeSpec) (ports.Worktree, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if spec.Detached {
		spec.Branch = ""
	}
	if existing, ok := w.byPath[spec.Path]; ok {
		if existing.Branch == spec.Branch && existing.Detached == spec.Detached {
			return existing, nil
		}
		return ports.Worktree{}, fmt.Errorf("%w: %s holds %s", ports.ErrWorktreeConflict, spec.Path, existing.Branch)
	}
	if holder, taken := w.branches[spec.Branch]; taken && !spec.Detached {
		return ports.Worktree{}, fmt.Errorf("%w: %s is checked out at %s", ports.ErrWorktreeConflict, spec.Branch, holder)
	}
	w.specs = append(w.specs, spec)
	head := LaneHead
	if spec.Base != "" {
		head = spec.Base
	}
	created := ports.Worktree{Path: spec.Path, Branch: spec.Branch, Head: head, Clean: true, Detached: spec.Detached}
	w.byPath[spec.Path] = created
	if !spec.Detached {
		w.branches[spec.Branch] = spec.Path
	}
	return created, nil
}

// CheckedOut names the worktree that has a branch checked out, or nothing.
func (w *Worktrees) CheckedOut(_ context.Context, _, branch string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.branches[branch], nil
}

// Inspect reads a worktree.
func (w *Worktrees) Inspect(_ context.Context, path string) (ports.Worktree, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entry, ok := w.byPath[path]
	if !ok {
		return ports.Worktree{}, fmt.Errorf("worktree %s: %w", path, ports.ErrNotFound)
	}
	return entry, nil
}

// Remove deletes a clean worktree.
func (w *Worktrees) Remove(_ context.Context, _, path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	entry, ok := w.byPath[path]
	if !ok {
		return nil
	}
	if !entry.Clean {
		return fmt.Errorf("%w: %s", ports.ErrWorktreeDirty, path)
	}
	delete(w.byPath, path)
	releaseBranch(w.branches, entry)
	return nil
}

// releaseBranch frees the branch a worktree held, and a detached one held none.
func releaseBranch(branches map[string]string, entry ports.Worktree) {
	if !entry.Detached {
		delete(branches, entry.Branch)
	}
}

// Discard deletes a worktree whatever it holds.
func (w *Worktrees) Discard(_ context.Context, _, path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if entry, ok := w.byPath[path]; ok {
		delete(w.byPath, path)
		releaseBranch(w.branches, entry)
	}
	return nil
}

// DirtyPath is the file a dirty worktree here says changed.
const DirtyPath = "changed.txt"

// Survey reads what a stop must account for.
func (w *Worktrees) Survey(_ context.Context, path string) (lanes.WorktreeFindings, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.survey != nil {
		return lanes.WorktreeFindings{}, w.survey
	}
	entry, ok := w.byPath[path]
	if !ok {
		return lanes.WorktreeFindings{}, fmt.Errorf("worktree %s: %w", path, ports.ErrNotFound)
	}
	if entry.Detached {
		return lanes.WorktreeFindings{Head: entry.Head}, nil
	}
	found := lanes.WorktreeFindings{
		Branch: entry.Branch, Head: entry.Head, Dirty: !entry.Clean, Remote: w.remote, Ahead: w.ahead[path],
	}
	if found.Dirty {
		found.DirtyPaths = []string{DirtyPath}
	}
	return found, nil
}

// Snapshot records which worktree a ref holds.
func (w *Worktrees) Snapshot(_ context.Context, path, ref string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.byPath[path]; !ok {
		return "", fmt.Errorf("worktree %s: %w", path, ports.ErrNotFound)
	}
	w.snapshots[ref] = path
	return "fake-snapshot", nil
}

// Push takes a ref, or refuses every one.
func (w *Worktrees) Push(_ context.Context, path, remote, ref string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.refuse || remote == "" {
		return ErrPushRefused
	}
	w.pushed = append(w.pushed, ref)
	if ref == lanes.HeadsRef(w.byPath[path].Branch) {
		w.ahead[path] = 0
	}
	return nil
}

// PushBranch takes a branch to the remote UseRemote named, or refuses it. With no remote it is
// ErrNoRemote.
func (w *Worktrees) PushBranch(_ context.Context, repository, branch string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.remote == "" {
		return fmt.Errorf("%w: %s", ports.ErrNoRemote, repository)
	}
	if w.refuse {
		return ErrPushRefused
	}
	w.pushed = append(w.pushed, lanes.HeadsRef(branch))
	return nil
}

// DefaultBranch names the branch every fake repository lands on.
func (w *Worktrees) DefaultBranch(context.Context, string) (string, error) { return "main", nil }

// MergeBranch merges a branch into its base, as git would, or comes to what MergeAs set. A merge asked again
// for the same reviewed commit finds the one it made.
func (w *Worktrees) MergeBranch(_ context.Context, _ string, merge ports.BranchMerge) (ledger.MergeResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.merges = append(w.merges, merge)
	if w.mergeAs != nil {
		return *w.mergeAs, nil
	}
	return ledger.MergeResult{Outcome: ledger.MergeMerged, MergeCommit: "merge-of-" + merge.Commit, MergedAt: "2026-01-02T03:04:05Z"}, nil
}

// MergeAs makes every branch merge come to a result, and nil lets them merge again.
func (w *Worktrees) MergeAs(result *ledger.MergeResult) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.mergeAs = result
}

// Merges lists every branch merge asked for, in order.
func (w *Worktrees) Merges() []ports.BranchMerge {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.merges)
}

// SeedFile puts a file in a worktree as committed work the lane's base holds, so a file added there later finds it.
func (w *Worktrees) SeedFile(path, name, text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.held == nil {
		w.held = map[string]map[string]string{}
	}
	if w.held[path] == nil {
		w.held[path] = map[string]string{}
	}
	w.held[path][name] = text
}

// FileText reads a file of a worktree, and whether the worktree holds it.
func (w *Worktrees) FileText(path, name string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	text, ok := w.held[path][name]
	return text, ok
}

// FailCommits makes every CommitFiles fail with err, until it is given nil.
func (w *Worktrees) FailCommits(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.commitFail = err
}

// CommitFiles adds the files to a worktree by the rules of ports.FileCommits, and keeps the commit it makes.
func (w *Worktrees) CommitFiles(_ context.Context, path string, files []ports.TreeFile, subject string) (ports.Commit, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.commitFail != nil {
		return ports.Commit{}, w.commitFail
	}
	if w.held == nil {
		w.held = map[string]map[string]string{}
	}
	if w.held[path] == nil {
		w.held[path] = map[string]string{}
	}
	var changed []string
	for _, file := range files {
		text, held := w.held[path][file.Path]
		var next string
		switch {
		case len(file.Lines) != 0:
			var added bool
			if next, added = withLines(text, file.Lines); !added {
				continue
			}
		case held:
			continue
		default:
			next = file.Text
		}
		w.held[path][file.Path] = next
		changed = append(changed, file.Path)
	}
	if len(changed) == 0 {
		return ports.Commit{}, nil
	}
	slices.Sort(changed)
	w.grown++
	commit := ports.Commit{Hash: fmt.Sprintf("file-commit-%d", w.grown), Subject: subject, Files: changed}
	w.fileCommits[path] = append([]ports.Commit{commit}, w.fileCommits[path]...)
	return commit, nil
}

// FailImports makes every ImportHistory fail with err, until it is given nil.
func (w *Worktrees) FailImports(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.importFail = err
}

// Imports lists every history import ImportHistory made, in order, and none it found made already.
func (w *Worktrees) Imports() []ports.HistoryImport {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.imports)
}

// ImportHistory merges nothing, and keeps the merge commit it would make by the rules of ports.HistoryImports: the
// commit has the history's tip as its second parent, lists the files asked for, and a repeat of an import the
// worktree holds is done and makes none.
func (w *Worktrees) ImportHistory(_ context.Context, path string, in ports.HistoryImport) (ports.Commit, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.importFail != nil {
		return ports.Commit{}, w.importFail
	}
	for _, made := range w.fileCommits[path] {
		if len(made.Parents) == 2 && made.Parents[1] == in.Commit {
			return ports.Commit{}, nil
		}
	}
	var files []string
	for _, file := range in.Files {
		files = append(files, file.Path)
	}
	slices.Sort(files)
	w.grown++
	commit := ports.Commit{Hash: fmt.Sprintf("import-%d", w.grown), Subject: in.Subject, Files: files, Parents: []string{"base-of-" + path, in.Commit}}
	w.fileCommits[path] = append([]ports.Commit{commit}, w.fileCommits[path]...)
	w.imports = append(w.imports, in)
	return commit, nil
}

// withLines adds each line the text does not hold, each on a line of its own, and says whether it added any.
func withLines(text string, lines []string) (string, bool) {
	held := map[string]bool{}
	for line := range strings.SplitSeq(text, "\n") {
		held[line] = true
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

// Commits lists the commits CommitFiles made in a worktree, newest first. The fake keeps no history beyond them,
// so a base says nothing, and a worktree it never made a commit in has none.
func (w *Worktrees) Commits(_ context.Context, path, _ string) ([]ports.Commit, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.fileCommits[path]), nil
}

var (
	_ ports.Worktrees      = (*Worktrees)(nil)
	_ ports.FileCommits    = (*Worktrees)(nil)
	_ ports.HistoryImports = (*Worktrees)(nil)
	_ ports.ReviewTrees    = (*Worktrees)(nil)
	_ ports.ChangeSizes    = (*Worktrees)(nil)
	_ ports.BranchMerger   = (*Worktrees)(nil)
)
