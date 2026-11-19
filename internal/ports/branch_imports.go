package ports

import "context"

// BranchImports is what a store of base reads offers to read the history a branch imported, so a plan approval
// lists what a consolidation merged and a merged branch can be checked for the history it brought (FR-ADOPT-13).
// A control plane whose base reads do not offer it lists the consolidation as unread.
type BranchImports interface {
	// BranchCommits lists the commits on head's first-parent line that base does not hold, newest first, each with
	// its subject and parents and no files, and at most a bounded number of them. An empty base lists the newest
	// commits of the line. A commit the repository does not hold is ErrNotFound.
	BranchCommits(ctx context.Context, repository, base, head string) ([]Commit, error)
	// CommitPaths lists the paths a commit holds, in order, at most limit of them, and says when it left some out.
	// A commit the repository does not hold is ErrNotFound.
	CommitPaths(ctx context.Context, repository, commit string, limit int) (paths []string, truncated bool, err error)
	// FirstParentRoot is the commit with no parent that a commit's first-parent line ends at, which names the
	// repository whose history it is (FR-REPO-14). A commit the repository does not hold is ErrNotFound.
	FirstParentRoot(ctx context.Context, repository, commit string) (string, error)
}
