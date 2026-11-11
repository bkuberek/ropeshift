package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
)

// Clone is a git clone on a worker's machine, with what git says of it.
type Clone struct {
	// Path is the top of the clone's working tree, as an absolute path.
	Path string
	// ID is the repository's identity in its project's records: its first commit, as a source names it.
	ID            string
	Remote        string
	DefaultBranch string
	// Directory is the base name of Path, which names a new record (ADR-0022).
	Directory string
}

// UnreadPath is a path that holds no clone to register, with why (FR-REPO-13).
type UnreadPath struct {
	Path   string
	Reason string
}

// Checkouts is a worker's own clones: where it finds them on its machine, and the path of its checkout of
// each repository it runs lanes in, by project, which it keeps in its configuration (ADR-0022). A path
// means nothing on another machine, so none is in a project's records.
type Checkouts interface {
	// Find lists the clones at paths: a path that is the top of a clone is that clone, and any other
	// directory holds the clones among its children, down to depth levels. A path with none comes back
	// unread, with why.
	Find(ctx context.Context, paths []string, depth int) ([]Clone, []UnreadPath, error)
	// Keep records path as this worker's checkout of a project's repository, in place of any other.
	Keep(ctx context.Context, project ledger.WorkspaceID, repository, path string) error
	// Forget drops this worker's checkout of a project's repository.
	Forget(ctx context.Context, project ledger.WorkspaceID, repository string) error
	// Of lists this worker's checkouts in a project, as the path of each repository's.
	Of(ctx context.Context, project ledger.WorkspaceID) (map[string]string, error)
}
