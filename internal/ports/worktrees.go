package ports

import (
	"context"
	"errors"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// Worktree refusals.
var (
	// ErrWorktreeDirty means a removal would lose uncommitted work.
	ErrWorktreeDirty = errors.New("the worktree has uncommitted work")
	// ErrWorktreeConflict means the path or the branch belongs to something else.
	ErrWorktreeConflict = errors.New("the path or branch is taken")
	// ErrNoRemote means a repository has no remote to push to.
	ErrNoRemote = errors.New("the repository has no remote")
)

// WorktreeSpec asks for one lane's worktree. An empty Base starts the branch at the
// repository's HEAD.
type WorktreeSpec struct {
	Repository string
	Path       string
	Branch     string
	Base       string
	// Detached checks Base out with no branch at all, and Branch is ignored. Nothing done in the
	// worktree can move a branch, and it is a place to read from and throw away, as a review lane's is.
	Detached bool
}

// Worktree is one worktree as it stands.
type Worktree struct {
	Path   string
	Branch string
	Head   string
	// Clean is false when a tracked or an untracked file differs from HEAD.
	Clean bool
	// Detached says the worktree was made with no branch, so it holds nothing to keep.
	Detached bool
}

// Diff is what a commit changed since a base.
type Diff struct {
	// Patch is the change as a unified diff.
	Patch string
	// Files are the paths it touches, in the order git lists them.
	Files []string
}

// Worktrees manages git worktrees.
type Worktrees interface {
	// Create makes a worktree, or returns the one that already matches, so a retry
	// after a crash converges. A path or branch held by something else is
	// ErrWorktreeConflict. A detached worktree makes no branch.
	Create(ctx context.Context, spec WorktreeSpec) (Worktree, error)
	// Inspect reads a worktree, or returns ErrNotFound.
	Inspect(ctx context.Context, path string) (Worktree, error)
	// Remove deletes a clean worktree and keeps its branch. Uncommitted work is
	// ErrWorktreeDirty, and a worktree already gone is done.
	Remove(ctx context.Context, repository, path string) error
	// Discard deletes a worktree whatever it holds, and keeps its branch. A worktree
	// already gone is done.
	Discard(ctx context.Context, repository, path string) error
	// Survey reads what a stop must account for, and changes nothing a session can see. An
	// absent worktree is ErrNotFound. A detached worktree holds nothing to account for.
	Survey(ctx context.Context, path string) (lanes.WorktreeFindings, error)
	// Snapshot records the worktree as it stands in a commit under ref, untracked files
	// included, and leaves the lane's index and branch alone. A snapshot of what the ref
	// already holds is that commit, and an earlier snapshot stays reachable from a later
	// one, so a repeat loses nothing.
	Snapshot(ctx context.Context, path, ref string) (string, error)
	// Push sends one ref to a remote under the same name, and never forces.
	Push(ctx context.Context, path, remote, ref string) error
	// PushBranch sends a branch of a repository to its remote under the same name: the branch's
	// upstream, else origin, else the first remote. It never forces and runs no repository hook, and a
	// repository with no remote is ErrNoRemote.
	PushBranch(ctx context.Context, repository, branch string) error
	// Fetch brings refs a remote holds into a repository under the same names, so a lane can
	// go on from work another machine pushed. A repository with no remote, and a ref the remote
	// does not hold, are done: a worktree then starts from what this machine has.
	Fetch(ctx context.Context, repository string, refs []string) error
	// DefaultBranch names the branch a repository's work lands on. It reads an upstream's
	// default first, and a repository with none has its current branch.
	DefaultBranch(ctx context.Context, repository string) (string, error)
	// WriteContext leaves a pack's text in a directory git does not see, and returns its path. It
	// repeats safely, and a survey, a snapshot, and a removal all read the worktree as if it were
	// not there.
	WriteContext(ctx context.Context, path, text string) (string, error)
}

// ReviewTrees is what a worktrees store offers a review lane beyond a lane's own worktree. A detached
// WorktreeSpec is honored: the commit is checked out with no branch, and a store without this makes a
// branch where a review must leave none, so a worker runs no review on a store that does not offer it.
type ReviewTrees interface {
	Worktrees
	// Diff reads what head changed since base, from the repository's own history. A commit the
	// repository does not hold is ErrNotFound.
	Diff(ctx context.Context, repository, base, head string) (Diff, error)
	// WriteContextFile leaves one more named file beside the pack, in the same directory and with the
	// same rules, and returns its path. The name is a bare file name.
	WriteContextFile(ctx context.Context, path, name, text string) (string, error)
	// FetchCommit brings a commit the repository does not hold from its remote, as a detached worktree's
	// creation does for a head another clone pushed. A commit the repository holds, or a repository with no
	// remote, needs nothing and is no error.
	FetchCommit(ctx context.Context, repository, commit string) error
}

// TreeFile is one file a restructuring adds to a worktree (FR-ADOPT-14). It is added only when the worktree
// holds nothing there already: a file whose text is given is written only when no file is at its path, and
// lines are added to a file only when it holds no line like each.
type TreeFile struct {
	// Path is relative to the worktree's top, with slashes.
	Path string
	// Text is the whole content of a file that is added when none is at Path.
	Text string
	// Lines are the lines added to the file at Path, which is made when none is there, in place of Text.
	Lines []string
}

// Commit is one commit of a worktree's branch.
type Commit struct {
	Hash    string
	Subject string
	// Files are the paths the commit changed, in the order git lists them. A merge commit lists what it changed
	// against its first parent.
	Files []string
	// Parents are the commits the commit grew from, the first parent first.
	Parents []string
}

// FileCommits is what a worktrees store offers to commit files to a lane's branch before the lane starts, so
// a worker can restructure the repository a lane plans in, and to find a commit it made. A worker whose store
// does not offer it restructures nothing.
type FileCommits interface {
	Worktrees
	// CommitFiles adds the files to the worktree at path and commits what that changed, as one commit with the
	// subject, in the name of the product and with no hook run and no signature asked for. A file the worktree
	// holds already is left as it is, and when every file is held already nothing is committed and the commit
	// that comes back is empty, so a retry after a crash that left the commit makes no second one.
	CommitFiles(ctx context.Context, path string, files []TreeFile, subject string) (Commit, error)
	// Commits lists the commits the worktree's branch holds beyond base, newest first, or its last few when
	// base is empty. A worktree that is gone is ErrNotFound.
	Commits(ctx context.Context, path, base string) ([]Commit, error)
	// BranchChanges lists the commits a branch of the repository holds beyond base, newest first, as Commits does
	// for a worktree's own branch, so a push is read in the checkout it is sent from. A repository that is gone, and a
	// branch or base it does not hold, are ErrNotFound.
	BranchChanges(ctx context.Context, repository, base, branch string) ([]Commit, error)
	// Tracks says the worktree's index holds a file at name, or under it when it names a folder, reading name
	// literally, so the worker never ignores a path whose new files a survey would then not see. A worktree that is
	// gone is ErrNotFound.
	Tracks(ctx context.Context, path, name string) (bool, error)
}

// HistoryImport asks for the history of another repository to be merged into a worktree's branch as an unrelated
// history, in one merge commit (FR-ADOPT-13).
type HistoryImport struct {
	// Source is the clone the history is read from. It is only read: nothing is written to it.
	Source string
	// Commit is the tip of the history to merge, which the fetch asks Source for by its ID.
	Commit string
	// Root is the first-parent root the history must begin at, and a history that begins elsewhere is
	// *HistoryRootError.
	Root string
	// LeaveOut names trees of the history, relative to its top, that the merge commit removes. A path the
	// branch held already is never removed.
	LeaveOut []string
	// Files are added to the merge commit by the rules of TreeFile, after the paths both histories hold are
	// settled.
	Files []TreeFile
	// Subject is the merge commit's subject.
	Subject string
}

// HistoryRootError says the history a merge was asked to import begins at another first commit than the one
// it was planned from, so the repository is not the one the plan read.
type HistoryRootError struct {
	// Want is the first-parent root the import named, and Got the one the fetched history has.
	Want, Got string
}

func (e *HistoryRootError) Error() string {
	return "the history to import begins at " + e.Got + ", and the plan named " + e.Want
}

// HistoryImports is what a worktrees store offers to import the history of another repository into a lane's
// branch before the lane starts, so a worker can consolidate a design repository into the workspace. A worker
// whose store does not offer it consolidates nothing.
type HistoryImports interface {
	FileCommits
	// ImportHistory fetches the commit from the source, checks that its first-parent root is the one named, and
	// merges it into the worktree's branch with the branch first and the history second, with no commit
	// rewritten. For every path both hold, the merge keeps the branch's version, except `.gitignore`, which
	// becomes the branch's lines followed by each of the history's lines the branch lacks, in order. It then
	// removes the trees left out, adds the files, and commits all of it as one merge commit with the subject, in
	// the name of the product and with no hook run and no signature asked for. A branch that holds the commit
	// already is done, and the commit that comes back is empty, so a retry after a crash that left the merge
	// makes no second one; a merge a crash left half made is made again from the start.
	ImportHistory(ctx context.Context, path string, in HistoryImport) (Commit, error)
}

// BranchTrees is what a worktrees store offers to find where a branch already stands, so a lane that began in a
// worktree goes on in it when the root a profile names moves, as it does at an upgrade. A worker whose store does
// not offer it places every worktree by the root in force.
type BranchTrees interface {
	// CheckedOut names the path of the linked worktree of a repository that has branch checked out, as
	// the store spells it, or nothing when no worktree does. The repository's own working tree is never
	// the answer, and neither is a registration whose directory is gone.
	CheckedOut(ctx context.Context, repository, branch string) (string, error)
}

// PathSnapshots is what a worktrees store offers to save only a lane's own files (FR-LANE-20). A worker whose store
// does not offer it snapshots the whole worktree and leaves nothing out.
type PathSnapshots interface {
	// SnapshotPaths is Snapshot of only the named paths: the commit holds HEAD with each named path as the worktree
	// has it, a path that ends in `/` naming a folder whole and a path that is gone recorded as removed. Paths are
	// relative to the worktree and read literally, and no path leaves HEAD's tree as it is.
	SnapshotPaths(ctx context.Context, path, ref string, paths []string) (string, error)
}

// ChangeSizes is what a worktrees store offers to measure a lane's change (FR-OBS-13). A worker whose store
// does not offer it records no change size.
type ChangeSizes interface {
	// ChangeSize counts the files changed and the lines added and removed between base and head, from the
	// history of the repository or worktree at path, a binary file counting as a changed file with no lines.
	// A commit the repository does not hold is ErrNotFound.
	ChangeSize(ctx context.Context, path, base, head string) (ledger.ChangeSize, error)
}

// BranchMerge asks for one reviewed branch merged into its base with git (ADR-0020). The branch must still
// be at Commit, the reviewed commit, and the merge lands on the base with Method.
type BranchMerge struct {
	Branch string
	Base   string
	Commit string
	Method ledger.MergeMethod
	// WorkOrder names the work the merge lands. The commit the merge makes on the base carries it and Commit
	// as trailers, which is how a merge repeated after a crash finds the one it made.
	WorkOrder string
	// Title is the subject of the commit a merge or a squash makes.
	Title string
}

// BranchMerger merges a reviewed branch into its base with git, in a temporary worktree detached at the
// base's tip and never in the checkout: with a remote, it pushes the result to the base branch without
// forcing; with none, it moves the local base branch only from the tip it merged onto, and never while a
// worktree has the base checked out. A merge an earlier attempt landed answers merged. A conflict, a branch
// no longer at the reviewed commit, and a push the remote refuses come back as merge outcomes; an error is
// one a later attempt asks again, such as a base that moved while the merge ran.
type BranchMerger interface {
	MergeBranch(ctx context.Context, repository string, merge BranchMerge) (ledger.MergeResult, error)
}
