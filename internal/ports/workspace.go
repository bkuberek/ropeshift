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

// FindWorkspace finds a project's workspace on this worker (FR-PROF-6): the folder the user's profile names for the
// project, where an empty one says the project has none here; otherwise the checkout of the planning repository,
// when another checkout of the project lies inside it at a path it ignores; otherwise none. Nothing is stored, so a
// moved clone or a new planning repository changes the answer for the next worktree only. An empty project is the
// installation's only project, and an empty planning repository names none.
//
// Every place that asks where a worktree goes, a lane's start, the doctor, and a registration's root check, asks
// through this function, so a workspace found through the planning clone counts in each.
func FindWorkspace(ctx context.Context, kept Checkouts, profile lanes.Profile, project, planningRepository string) (lanes.Workspace, bool, error) {
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

// InsideWorkspace says a repository's clone lies inside the workspace and is not the workspace itself, so its
// built-in root is the workspace's folder for worktrees.
func InsideWorkspace(ctx context.Context, kept Checkouts, workspace, repository string) (bool, error) {
	if SamePath(ctx, kept, workspace, repository) {
		return false, nil
	}
	_, inside, err := InsideClone(ctx, kept, []string{workspace}, repository, "")
	return inside, err
}

// WorkspaceSettings gives the worktree settings a new worktree of a clone is placed by, when the clone's project
// has a workspace: the settings as they are, or, for a built-in root of a clone inside the workspace, with the
// workspace's folder for worktrees as the root. The workspace's own clone, a clone outside it, and a root a person
// set keep the settings. It names the workspace as unignored when the built-in root stays outside every clone
// because the workspace does not ignore that folder.
func WorkspaceSettings(ctx context.Context, kept Checkouts, settings lanes.WorktreeSettings, workspace lanes.Workspace, repository string) (lanes.WorktreeSettings, string, error) {
	if settings.RootSource != lanes.RootFromDefault {
		return settings, "", nil
	}
	inside, err := InsideWorkspace(ctx, kept, workspace.Path, repository)
	switch {
	case err != nil || !inside:
		return settings, "", err
	case workspace.Ignored:
		settings.Root = lanes.WorkspaceRoot(workspace.Path)
		return settings, "", nil
	}
	return settings, workspace.Path, nil
}
