package worker

import (
	"context"
	"errors"
	"fmt"
	"path"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// WorktreeInsideClone means a lane's worktree root is a clone this worker keeps, or lies inside one, so
// a worktree there would sit in a clone's working tree (FR-PROF-3, FR-REPO-15). The lane fails with the
// reason `worktree_inside_clone`, and its detail says how to fix it.
type WorktreeInsideClone struct {
	// Root is the lane's worktree root, as the profile resolved it for this lane.
	Root string
	// Clone is the clone it is, or lies inside.
	Clone string
	// Source names the layer that set the root, as lanes.RootFromUser does.
	Source string
	// Unignored names the workspace whose .ropeshift/worktrees/ folder the root lies under while the workspace
	// does not ignore it, so the detail names the line to add. Empty otherwise.
	Unignored string
}

// Error names the refusal.
func (e *WorktreeInsideClone) Error() string {
	return fmt.Sprintf("worktree root %s is inside the clone %s", e.Root, e.Clone)
}

// Detail says what the person sees in Needs you and in `ropeshift work show`: the root, the clone, the layer
// that set the root, and the fix. A root the built-in layer chose lies under a worktree directory a person
// put inside a clone, so its fix moves that directory.
func (e *WorktreeInsideClone) Detail() string {
	fix := lanes.RootFix(e.Source)
	if e.Unignored != "" {
		fix = fmt.Sprintf("add `%s` to the .gitignore of %s, or %s", planning.WorktreesLine, e.Unignored, fix)
	}
	return fmt.Sprintf("worktree root %s is inside the clone %s, and %s set it: %s", e.Root, e.Clone, e.Source, fix)
}

// Notice says, in a line, that a lane went on in a worktree it already had and where a new one would go.
func (k KeptWorktree) Notice() string {
	notice := "goes on in its existing worktree " + k.Path
	if k.Would != k.Path {
		notice += ", not " + k.Would + " where the root in force puts a new one"
	}
	if k.Inside != "" {
		notice += "; it lies inside the clone " + k.Inside + ", where a new worktree is refused, and stays until the lane ends"
	}
	return notice
}

// place says where a lane's worktree goes. A lane that already has a worktree of its own goes on in it, wherever
// it is, because an upgrade can move the default root under a lane in flight and a second worktree for the same
// branch cannot be made. The root in force then applies only to a worktree not yet made, which is refused when it
// lies inside any clone this worker uses, before anything exists there: a clone it keeps in any project, a source
// clone the daemon reads, or the repository the lane works in. A worktree that exists is never refused: the lane
// goes on in it and the second result says so. A worktree that is not the lane's own is never taken, so a
// branch another worktree holds still refuses the start.
//
// A clone inside the project's workspace takes the built-in root in the workspace's .ropeshift/worktrees/ folder
// when the workspace ignores it, and the root outside every clone, with the workspace named for the start's
// notice, when it does not. The workspace's own lanes, a clone outside it, and a root a person set are not moved.
func (p Provisioner) place(ctx context.Context, request LaneRequest, profile lanes.Profile) (lanes.Placement, *KeptWorktree, lanes.RootFallback, error) {
	none := lanes.RootFallback{}
	settings, workspace, fallback, err := p.workspaceRoot(ctx, request, profile)
	if err != nil {
		return lanes.Placement{}, nil, none, err
	}
	place, err := lanes.Place(settings, request.Repository, request.Project, request.Lane, request.Branch)
	if err != nil {
		return place, nil, none, err
	}
	held, err := p.ownPlace(ctx, request, place)
	if err != nil {
		return lanes.Placement{}, nil, none, err
	}
	var kept *KeptWorktree
	if held != "" && !p.samePlace(ctx, held, place.Path) {
		kept = &KeptWorktree{Path: held, Would: place.Path}
	}
	if held != "" {
		place.Path = held
	}
	if p.Checkouts == nil {
		return place, kept, fallback, nil
	}
	clones, err := p.Checkouts.Every(ctx)
	if err != nil {
		return lanes.Placement{}, nil, none, err
	}
	clones = append(append(clones, p.Sources...), request.Repository)
	allowed := ""
	if workspace.Clone {
		allowed = workspace.Path
	}
	clone, inside, err := ports.InsideClone(ctx, p.Checkouts, clones, place.Path, allowed)
	if err != nil {
		return lanes.Placement{}, nil, none, err
	}
	if inside && workspace.Path == "" && profile.Worktree.RootSource != lanes.RootFromDefault {
		if workspace, err = p.allowance(ctx, request, profile); err != nil {
			return lanes.Placement{}, nil, none, err
		}
		if workspace.Clone {
			if clone, inside, err = ports.InsideClone(ctx, p.Checkouts, clones, place.Path, workspace.Path); err != nil {
				return lanes.Placement{}, nil, none, err
			}
		}
	}
	switch {
	case inside && held != "":
		if kept == nil {
			kept = &KeptWorktree{Path: held, Would: place.Path}
		}
		kept.Inside = clone
	case inside:
		refusal := &WorktreeInsideClone{Root: path.Dir(place.Path), Clone: clone, Source: profile.Worktree.RootSource}
		if refusal.Unignored, err = p.unignoredFolder(ctx, workspace, clone, place.Path); err != nil {
			return lanes.Placement{}, nil, none, err
		}
		return lanes.Placement{}, nil, none, refusal
	}
	return place, kept, fallback, nil
}

// workspaceRoot gives the worktree settings a new worktree of the lane is placed by: the profile's own, or, for a
// built-in root of a clone inside the project's workspace, the workspace's folder for worktrees. It returns the
// workspace it found, and says why the built-in root fell back to the root outside every clone when it did.
func (p Provisioner) workspaceRoot(ctx context.Context, request LaneRequest, profile lanes.Profile) (lanes.WorktreeSettings, lanes.Workspace, lanes.RootFallback, error) {
	none := lanes.RootFallback{}
	// A root a person set is never moved into a workspace, so such a lane has no use for the workspace here, and a
	// workspace that cannot be checked must not stop it. A root of the person's inside the workspace's clone asks
	// for the workspace later, where it is refused or allowed.
	if p.Checkouts == nil || profile.Worktree.RootSource != lanes.RootFromDefault {
		return profile.Worktree, lanes.Workspace{}, none, nil
	}
	workspace, found, err := FindWorkspace(ctx, p.Checkouts, profile, request.Project, request.PlanningRepository)
	if err != nil {
		return p.unreadable(ctx, profile, err)
	}
	if !found {
		return profile.Worktree, lanes.Workspace{}, none, nil
	}
	settings, fallback, err := ports.NewWorkspace(p.Checkouts).Settings(ctx, profile.Worktree, workspace, request.Repository)
	if err != nil {
		return p.unreadable(ctx, profile, err)
	}
	return settings, workspace, fallback, nil
}

// unreadable keeps the built-in root outside every clone for a lane whose workspace could not be checked, and says
// what went wrong in the lane's start notice. A lane that git's answer for a workspace stops would otherwise never
// start, for a root it does not use. A cancelled context is not a workspace's trouble, and stops the lane.
func (p Provisioner) unreadable(ctx context.Context, profile lanes.Profile, err error) (lanes.WorktreeSettings, lanes.Workspace, lanes.RootFallback, error) {
	if ctx.Err() != nil {
		return lanes.WorktreeSettings{}, lanes.Workspace{}, lanes.RootFallback{}, ctx.Err()
	}
	return profile.Worktree, lanes.Workspace{}, lanes.RootFallback{Reason: lanes.FallbackUnreadable, Detail: err.Error()}, nil
}

// allowance finds the workspace of a lane whose root a person set and lies inside a clone, because that root is
// allowed inside the workspace's clone under its ignored folder for worktrees. A workspace that cannot be found or
// checked gives no allowance, so the root is refused as a root inside the clone.
func (p Provisioner) allowance(ctx context.Context, request LaneRequest, profile lanes.Profile) (lanes.Workspace, error) {
	workspace, found, err := FindWorkspace(ctx, p.Checkouts, profile, request.Project, request.PlanningRepository)
	if ctx.Err() != nil {
		return lanes.Workspace{}, ctx.Err()
	}
	if err != nil || !found {
		return lanes.Workspace{}, nil
	}
	return workspace, nil
}

// unignoredFolder names the workspace when a refused path lies under its folder for worktrees, which the workspace
// does not ignore, so the refusal can say which line to add.
func (p Provisioner) unignoredFolder(ctx context.Context, workspace lanes.Workspace, clone, target string) (string, error) {
	if !workspace.Clone || !ports.SamePath(ctx, p.Checkouts, clone, workspace.Path) {
		return "", nil
	}
	under, err := ports.UnderWorkspaceWorktrees(ctx, p.Checkouts, workspace.Path, target)
	if err != nil || !under {
		return "", err
	}
	return workspace.Path, nil
}

// ownPlace names the worktree that is this lane's own, or nothing. A worktree is the lane's when its folder is
// named for the lane, as every worktree the worker makes is, and it is the one the lane's journal recorded when
// the journal recorded one. A lane with a branch finds it by the branch it holds, which a worktree of a
// person's or of another lane's can hold too and is then not the lane's. A review lane has no branch, so its
// own is the one recorded, when that stands and is detached. A store that cannot say finds nothing.
func (p Provisioner) ownPlace(ctx context.Context, request LaneRequest, place lanes.Placement) (string, error) {
	if request.Detached {
		if request.Recorded == "" || path.Base(request.Recorded) != request.Lane {
			return "", nil
		}
		held, err := p.Worktrees.Inspect(ctx, request.Recorded)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && !held.Detached) {
			return "", nil
		}
		return request.Recorded, err
	}
	trees, ok := p.Worktrees.(ports.BranchTrees)
	if !ok || place.Branch == "" {
		return "", nil
	}
	held, err := trees.CheckedOut(ctx, request.Repository, place.Branch)
	if err != nil || held == "" || path.Base(held) != request.Lane {
		return "", err
	}
	if request.Recorded != "" && !p.samePlace(ctx, held, request.Recorded) {
		return "", nil
	}
	return held, nil
}

// samePlace says two paths name one directory, spelled the same or, as a disk that ignores case or a link
// allows, another way.
func (p Provisioner) samePlace(ctx context.Context, a, b string) bool {
	if a == b {
		return true
	}
	if p.Checkouts == nil {
		return false
	}
	same, err := p.Checkouts.SameFile(ctx, a, b)
	return err == nil && same
}
