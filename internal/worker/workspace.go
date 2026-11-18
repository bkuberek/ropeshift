package worker

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
	"ropeshift.com/ropeshift/internal/ports"
)

// FindWorkspace finds a project's workspace on this worker (FR-PROF-6): the folder the user's profile names for the
// project, where an empty one says the project has none here; otherwise the checkout of the planning repository,
// when another checkout of the project lies inside it at a path it ignores; otherwise none. Nothing is stored, so a
// moved clone or a new planning repository changes the answer for the next worktree only. An empty project is the
// installation's only project, and an empty planning repository names none.
func FindWorkspace(ctx context.Context, kept ports.Checkouts, profile lanes.Profile, project, planningRepository string) (lanes.Workspace, bool, error) {
	if project == "" {
		project = lanes.DefaultProject
	}
	if named := profile.Projects[project].Workspace; named != nil {
		if *named == "" {
			return lanes.Workspace{}, false, nil
		}
		return checked(ctx, kept, lanes.Workspace{Path: *named, Found: lanes.WorkspaceFromProfile})
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
			return checked(ctx, kept, lanes.Workspace{Path: home, Found: lanes.WorkspaceFromPlanning})
		}
	}
	return lanes.Workspace{}, false, nil
}

// ignoredBelow says a checkout lies inside the planning clone, at a path its repository ignores. The checkout is
// the planning clone itself, or outside it, when no path inside is named.
func ignoredBelow(ctx context.Context, kept ports.Checkouts, home, checkout string) (bool, error) {
	if ports.SamePath(ctx, kept, home, checkout) {
		return false, nil
	}
	_, inside, err := ports.InsideClone(ctx, kept, []string{home}, checkout, "")
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
func relativeTo(ctx context.Context, kept ports.Checkouts, dir, inside string) (string, error) {
	top, _, err := kept.Resolve(ctx, dir)
	if errors.Is(err, ports.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	real, _, err := kept.Resolve(ctx, inside)
	if errors.Is(err, ports.ErrNotFound) {
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

// checked says whether lane worktrees may go in a workspace's folder for them. A workspace that is not a git clone
// needs no check, and one that is must ignore the folder, which git answers for a directory pattern before the
// folder exists.
func checked(ctx context.Context, kept ports.Checkouts, found lanes.Workspace) (lanes.Workspace, bool, error) {
	clones, _, err := kept.Find(ctx, []string{found.Path}, 0)
	if err != nil {
		return lanes.Workspace{}, false, err
	}
	found.Clone = slices.ContainsFunc(clones, func(clone ports.Clone) bool { return ports.SamePath(ctx, kept, clone.Path, found.Path) })
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
func insideWorkspace(ctx context.Context, kept ports.Checkouts, workspace, repository string) (bool, error) {
	if ports.SamePath(ctx, kept, workspace, repository) {
		return false, nil
	}
	_, inside, err := ports.InsideClone(ctx, kept, []string{workspace}, repository, "")
	return inside, err
}
