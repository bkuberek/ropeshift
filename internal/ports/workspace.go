package ports

import (
	"context"
	"errors"
	"maps"
	"path"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
)

// Workspace answers where a project's workspace is on a worker's machine and what that means for a clone's lane
// worktrees (FR-PROF-6). Every place that asks where a worktree goes, a lane's start, the doctor, and a
// registration's root check, asks through it, so a workspace found through the planning clone counts in each.
type Workspace interface {
	// Find finds a project's workspace: the folder the user's profile names for the project, where an empty one
	// says the project has none here; otherwise the checkout of the planning repository, when another checkout of
	// the project lies inside it at a path it ignores; otherwise none. Nothing is stored, so a moved clone or a new
	// planning repository changes the answer for the next worktree only. An empty project is the installation's
	// only project, and an empty planning repository names none.
	Find(ctx context.Context, profile lanes.Profile, project, planningRepository string) (lanes.Workspace, bool, error)
	// Settings gives the worktree settings a new worktree of a clone is placed by, when the clone's project has a
	// workspace: the settings as they are, or, for a built-in root of a clone inside the workspace, with the
	// workspace's folder for worktrees as the root. The workspace's own clone, a clone outside it, and a root a
	// person set keep the settings. The fallback says why the built-in root of a clone inside the workspace stays
	// outside every clone: the workspace does not ignore its folder for worktrees, or a symbolic link leads the
	// folder the lane would use, or the lane's own folder, out of the workspace, resolved as git and the disk
	// resolve it. A caller with no lane of its own, such as the doctor, passes a probe name.
	Settings(ctx context.Context, settings lanes.WorktreeSettings, workspace lanes.Workspace, repository, lane string) (lanes.WorktreeSettings, lanes.RootFallback, error)
}

// NewWorkspace answers from the clones a worker keeps, and from what git says each ignores.
func NewWorkspace(kept Checkouts) Workspace { return worksFrom{kept: kept} }

type worksFrom struct{ kept Checkouts }

func (w worksFrom) Find(ctx context.Context, profile lanes.Profile, project, planningRepository string) (lanes.Workspace, bool, error) {
	return findWorkspace(ctx, w.kept, profile, project, planningRepository)
}

func (w worksFrom) Settings(ctx context.Context, settings lanes.WorktreeSettings, workspace lanes.Workspace, repository, lane string) (lanes.WorktreeSettings, lanes.RootFallback, error) {
	return workspaceSettings(ctx, w.kept, settings, workspace, repository, lane)
}

func findWorkspace(ctx context.Context, kept Checkouts, profile lanes.Profile, project, planningRepository string) (lanes.Workspace, bool, error) {
	if project == "" {
		project = lanes.DefaultProject
	}
	if named := profile.Projects[project].Workspace; named != nil {
		if *named == "" {
			return lanes.Workspace{}, false, nil
		}
		return checkedWorkspace(ctx, kept, lanes.Workspace{Path: *named, Found: lanes.WorkspaceFromProfile})
	}
	if planningRepository == "" {
		return lanes.Workspace{}, false, nil
	}
	held, err := kept.Of(ctx, ledger.WorkspaceID(project))
	if err != nil {
		return lanes.Workspace{}, false, err
	}
	home, ok := held[planningRepository]
	if !ok {
		return lanes.Workspace{}, false, nil
	}
	for _, id := range slices.Sorted(maps.Keys(held)) {
		if id == planningRepository {
			continue
		}
		inside, err := ignoredBelow(ctx, kept, home, held[id])
		if err != nil {
			return lanes.Workspace{}, false, err
		}
		if inside {
			return checkedWorkspace(ctx, kept, lanes.Workspace{Path: home, Found: lanes.WorkspaceFromPlanning})
		}
	}
	return lanes.Workspace{}, false, nil
}

// ignoredBelow says a checkout lies inside the planning clone, at a path its repository ignores. The checkout is
// the planning clone itself, or outside it, when no path inside is named.
func ignoredBelow(ctx context.Context, kept Checkouts, home, checkout string) (bool, error) {
	if SamePath(ctx, kept, home, checkout) {
		return false, nil
	}
	_, inside, err := InsideClone(ctx, kept, []string{home}, checkout, "")
	if err != nil || !inside {
		return false, err
	}
	relative, err := relativeTo(ctx, kept, home, checkout)
	if err != nil || relative == "" {
		return false, err
	}
	return kept.Ignored(ctx, home, relative)
}

// relativeTo spells a path inside a directory relative to it, both with their links followed, or nothing when it
// does not lie inside.
func relativeTo(ctx context.Context, kept Checkouts, dir, inside string) (string, error) {
	top, _, err := kept.Resolve(ctx, dir)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	real, _, err := kept.Resolve(ctx, inside)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	relative, found := strings.CutPrefix(real, path.Clean(top)+"/")
	if !found {
		return "", nil
	}
	return relative, nil
}

// checkedWorkspace says whether lane worktrees may go in a workspace's folder for them. A workspace that is not a
// git clone needs no check, and one that is must ignore the folder, which git answers for a directory pattern
// before the folder exists.
func checkedWorkspace(ctx context.Context, kept Checkouts, found lanes.Workspace) (lanes.Workspace, bool, error) {
	clones, _, err := kept.Find(ctx, []string{found.Path}, 0)
	if err != nil {
		return lanes.Workspace{}, false, err
	}
	found.Clone = slices.ContainsFunc(clones, func(clone Clone) bool { return SamePath(ctx, kept, clone.Path, found.Path) })
	found.Ignored = true
	if found.Clone {
		if found.Ignored, err = kept.Ignored(ctx, found.Path, planning.WorktreesFolder+"/"); err != nil {
			return lanes.Workspace{}, false, err
		}
	}
	return found, true, nil
}

// insideWorkspace says a repository's clone lies inside the workspace and is not the workspace itself, so its
// built-in root is the workspace's folder for worktrees.
func insideWorkspace(ctx context.Context, kept Checkouts, workspace, repository string) (bool, error) {
	if SamePath(ctx, kept, workspace, repository) {
		return false, nil
	}
	_, inside, err := InsideClone(ctx, kept, []string{workspace}, repository, "")
	return inside, err
}

func workspaceSettings(ctx context.Context, kept Checkouts, settings lanes.WorktreeSettings, workspace lanes.Workspace, repository, lane string) (lanes.WorktreeSettings, lanes.RootFallback, error) {
	if settings.RootSource != lanes.RootFromDefault {
		return settings, lanes.RootFallback{}, nil
	}
	inside, err := insideWorkspace(ctx, kept, workspace.Path, repository)
	switch {
	case err != nil || !inside:
		return settings, lanes.RootFallback{}, err
	case !workspace.Ignored:
		return settings, lanes.RootFallback{Workspace: workspace.Path, Reason: lanes.FallbackUnignored}, nil
	}
	// The lane's own folder must really be in the workspace: a link planted for the lane's folder, for the
	// repository's folder, for the folder of worktrees, or for .ropeshift would lead the lane out of it, past the
	// ignore rule that guarded the path as spelled. Every link in the path is followed, so judging the lane's folder
	// judges each folder above it. A plain folder, which has no ignore rule, is held to it too.
	folder := path.Join(workspace.Path, planning.WorktreesFolder, path.Base(repository), lane)
	contained, err := UnderWorkspaceWorktrees(ctx, kept, workspace.Path, folder)
	switch {
	case err != nil:
		return settings, lanes.RootFallback{}, err
	case !contained:
		return settings, lanes.RootFallback{Workspace: workspace.Path, Reason: lanes.FallbackEscapes}, nil
	}
	settings.Root = lanes.WorkspaceRoot(workspace.Path)
	return settings, lanes.RootFallback{}, nil
}
