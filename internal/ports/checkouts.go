package ports

import (
	"context"
	"errors"
	"path"

	"ropeshift.com/ropeshift/internal/ledger"
)

// ErrRepositoryReadsUnavailable says a control plane reads no repository on its own machine: a group
// server keeps no clone, so each read that solo mode makes from one is refused, and the error that wraps
// this one names the read.
var ErrRepositoryReadsUnavailable = errors.New("repository_reads_unavailable")

// Clone is a git clone on a worker's machine, with what git says of it.
type Clone struct {
	// Path is the top of the clone's working tree, as an absolute path.
	Path string
	// Entered is the path the clone was reached by, as the member named it or as found below a path they
	// named, before symbolic links were followed. It is for display only: records and checkouts name the clone
	// by Path. Empty reads as Path.
	Entered string
	// ID is the repository's identity in its project's records: the root commit reached by following first
	// parents from its default branch tip, as a source names it (FR-REPO-14).
	ID string
	// Roots is every first commit the clone holds, sorted, ID among them. A clone that merged an unrelated
	// history holds more than one, and a record stored under the lowest-root rule is named by the lowest.
	// Empty reads as ID alone.
	Roots         []string
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

// Checkout is this worker's checkout of one repository of a project.
type Checkout struct {
	Project    ledger.WorkspaceID
	Repository string
	Path       string
}

// Checkouts is a worker's own clones: where it finds them on its machine, and the path of its checkout of
// each repository it runs lanes in, by project, which it keeps in its configuration (ADR-0022). A path
// means nothing on another machine, so none is in a project's records.
type Checkouts interface {
	// Find lists the clones at paths: a path that is the top of a clone is that clone, and any other
	// directory holds the clones among its children, down to depth levels. A path with none comes back
	// unread, with why.
	Find(ctx context.Context, paths []string, depth int) ([]Clone, []UnreadPath, error)
	// Resolve follows every symbolic link in an absolute path on this machine, to the path it comes to.
	// Reserved says that path is inside a directory the installation keeps for itself, such as its state or
	// run directory, which holds no clone to register. A path nothing is at is ErrNotFound.
	Resolve(ctx context.Context, path string) (real string, reserved bool, err error)
	// SameFile says two absolute paths on this machine name one file or directory, by what the disk says of
	// each, so two spellings of one directory, as a disk that ignores case allows, are one. A path nothing is
	// at is ErrNotFound.
	SameFile(ctx context.Context, a, b string) (same bool, err error)
	// Ignored says the git repository whose clone is at clone ignores path, a path inside the clone, written
	// relative to it. A folder the repository ignores ignores everything in it. Git failing to answer is an
	// error, and is never read as a path that is not ignored.
	Ignored(ctx context.Context, clone, path string) (ignored bool, err error)
	// Keep records path as this worker's checkout of a project's repository, in place of any other.
	Keep(ctx context.Context, project ledger.WorkspaceID, repository, path string) error
	// Forget drops this worker's checkout of a project's repository.
	Forget(ctx context.Context, project ledger.WorkspaceID, repository string) error
	// Of lists this worker's checkouts in a project, as the path of each repository's.
	Of(ctx context.Context, project ledger.WorkspaceID) (map[string]string, error)
	// Every lists the path of every checkout this worker keeps, in every project, each once and in order.
	// No lane's worktree may be one of them or inside one.
	Every(ctx context.Context) ([]string, error)
	// All lists every checkout this worker keeps with its project and repository, by project and then
	// repository.
	All(ctx context.Context) ([]Checkout, error)
}

// InsideClone says whether a path is one of the clones or lies inside one, and names that clone. It
// compares what the disk says of each path, every symbolic link followed, so a root that reaches a clone
// through a link, or that a disk that ignores case spells another way, is found. A path nothing is at is
// judged by the nearest directory above it that exists, because a worktree's root is made after the check.
// A clone nothing is at holds nothing.
func InsideClone(ctx context.Context, kept Checkouts, clones []string, target string) (string, bool, error) {
	real, err := resolveExisting(ctx, kept, target)
	if err != nil {
		return "", false, err
	}
	for _, clone := range clones {
		for at := real; ; at = path.Dir(at) {
			same, err := kept.SameFile(ctx, at, clone)
			switch {
			case errors.Is(err, ErrNotFound):
			case err != nil:
				return "", false, err
			case same:
				return clone, true, nil
			}
			if path.Dir(at) == at {
				break
			}
		}
	}
	return "", false, nil
}

// resolveExisting follows every link in the part of a path that exists and keeps the rest as it is spelled.
func resolveExisting(ctx context.Context, kept Checkouts, target string) (string, error) {
	at, rest := path.Clean(target), ""
	for {
		real, _, err := kept.Resolve(ctx, at)
		if err == nil {
			return path.Join(real, rest), nil
		}
		parent := path.Dir(at)
		if !errors.Is(err, ErrNotFound) || parent == at {
			return "", err
		}
		rest, at = path.Join(path.Base(at), rest), parent
	}
}
