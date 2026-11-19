package ports

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
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
	// DanglingLink walks the components of target below the directory below, each read as it is spelled and not
	// followed, and names the first that is a symbolic link leading to nothing, or "" when none is. A component
	// nothing is at ends the walk, as nothing lies beyond it. Resolve cannot tell such a link from a path nothing is
	// at, and a tool that creates the folder makes it at the link's target.
	DanglingLink(ctx context.Context, below, target string) (link string, err error)
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
	// ForgetAt drops this worker's checkout of a project's repository only while it is still kept at path, and says
	// whether it dropped it, so a checkout kept again at another path in the meantime stays.
	ForgetAt(ctx context.Context, project ledger.WorkspaceID, repository, path string) (bool, error)
	// Of lists this worker's checkouts in a project, as the path of each repository's.
	Of(ctx context.Context, project ledger.WorkspaceID) (map[string]string, error)
	// Every lists the path of every checkout this worker keeps, in every project, each once and in order.
	// No lane's worktree may be one of them or inside one.
	Every(ctx context.Context) ([]string, error)
	// All lists every checkout this worker keeps with its project and repository, by project and then
	// repository.
	All(ctx context.Context) ([]Checkout, error)
}

// RopeshiftFolder is the name of the folder a repository keeps ropeshift's own files in, its profile layer and,
// in a workspace, the lane worktrees under it (FR-PROF-6).
const RopeshiftFolder = ".ropeshift"

// InRopeshiftFolder says a path is or lies inside a .ropeshift folder. No lane worktree there is a clone to find
// or to register, in a scan or by name.
func InRopeshiftFolder(at string) bool {
	return slices.Contains(strings.Split(path.Clean(at), "/"), RopeshiftFolder)
}

// SamePath says two paths name one directory on this worker's machine, however each is spelled: a checkout kept
// through a symbolic link, as a temporary directory is on a machine whose /var is a link, is the clone at the
// path the link leads to. A machine that cannot tell says they differ.
func SamePath(ctx context.Context, kept Checkouts, a, b string) bool {
	if a == b {
		return true
	}
	if kept == nil {
		return false
	}
	same, err := kept.SameFile(ctx, a, b)
	return err == nil && same
}

// InsideClone says whether a path is one of the clones or lies inside one, and names that clone. It
// compares what the disk says of each path, every symbolic link followed, so a root that reaches a clone
// through a link, or that a disk that ignores case spells another way, is found. A path nothing is at is
// judged by the nearest directory above it that exists, because a worktree's root is made after the check.
// A clone nothing is at holds nothing.
//
// workspace is the path of the project's workspace when it is a clone, or empty. A path inside that clone is
// allowed when it lies under the clone's own .ropeshift/worktrees/ folder and the clone ignores that folder
// (FR-PROF-3). Every other path inside it, and every path inside any other clone, the project's code clones
// inside the workspace included, is inside a clone as before.
func InsideClone(ctx context.Context, kept Checkouts, clones []string, target, workspace string) (string, bool, error) {
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
				open, err := openedByWorkspace(ctx, kept, clone, workspace, real)
				if err != nil {
					return "", false, err
				}
				if !open {
					return clone, true, nil
				}
			}
			if path.Dir(at) == at || same {
				break
			}
		}
	}
	return "", false, nil
}

// openedByWorkspace says a path inside clone is allowed because clone is the workspace, the path lies under its
// folder for worktrees, and the clone ignores that folder.
func openedByWorkspace(ctx context.Context, kept Checkouts, clone, workspace, real string) (bool, error) {
	if workspace == "" {
		return false, nil
	}
	isWorkspace, err := kept.SameFile(ctx, clone, workspace)
	switch {
	case errors.Is(err, ErrNotFound):
		return false, nil
	case err != nil || !isWorkspace:
		return false, err
	}
	under, err := underWorktreeFolder(ctx, kept, workspace, real)
	if err != nil || !under {
		return false, err
	}
	return kept.Ignored(ctx, workspace, planning.WorktreesFolder+"/")
}

// LeadsOutOfWorkspace says a path meant to lie under a workspace's .ropeshift/worktrees/ folder does not, once its
// links are followed: it resolves outside the folder, or a link in it below the workspace leads to nothing, which
// resolves to no place at all and so passes for a path that does not exist yet.
func LeadsOutOfWorkspace(ctx context.Context, kept Checkouts, workspace, target string) (bool, error) {
	dangling, err := kept.DanglingLink(ctx, path.Clean(workspace), path.Clean(target))
	if err != nil || dangling != "" {
		return dangling != "", err
	}
	under, err := UnderWorkspaceWorktrees(ctx, kept, workspace, target)
	return !under, err
}

// UnderWorkspaceWorktrees says a path lies under a workspace's .ropeshift/worktrees/ folder, whether or not the
// workspace ignores it, so a refusal can name the line to add.
func UnderWorkspaceWorktrees(ctx context.Context, kept Checkouts, workspace, target string) (bool, error) {
	real, err := resolveExisting(ctx, kept, target)
	if err != nil {
		return false, err
	}
	return underWorktreeFolder(ctx, kept, workspace, real)
}

// underWorktreeFolder compares a real path with the workspace's folder for worktrees, every link followed in both.
func underWorktreeFolder(ctx context.Context, kept Checkouts, workspace, real string) (bool, error) {
	root, _, err := kept.Resolve(ctx, workspace)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.HasPrefix(real+"/", path.Join(root, planning.WorktreesFolder)+"/"), nil
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
