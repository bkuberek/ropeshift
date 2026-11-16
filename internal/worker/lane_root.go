package worker

import (
	"context"
	"errors"
	"fmt"
	"path"

	"ropeshift.com/ropeshift/internal/lanes"
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
}

// Error names the refusal.
func (e *WorktreeInsideClone) Error() string {
	return fmt.Sprintf("worktree root %s is inside the clone %s", e.Root, e.Clone)
}

// Detail says what the person sees in Needs you and in `ropeshift work show`: the root, the clone, the layer
// that set the root, and the fix. A root the built-in layer chose lies under a worktree directory a person
// put inside a clone, so its fix moves that directory.
func (e *WorktreeInsideClone) Detail() string {
	if e.Source == lanes.RootFromDefault {
		return fmt.Sprintf("worktree root %s is inside the clone %s, and %s set it: set ROPESHIFT_WORKTREE_DIR to a folder outside every clone",
			e.Root, e.Clone, e.Source)
	}
	return fmt.Sprintf("worktree root %s is inside the clone %s, and %s set it: set `[worktree] root` in your profile to a folder outside every clone, or remove it to use the built-in root",
		e.Root, e.Clone, e.Source)
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
func (p Provisioner) place(ctx context.Context, request LaneRequest, profile lanes.Profile) (lanes.Placement, *KeptWorktree, error) {
	place, err := lanes.Place(profile.Worktree, request.Repository, request.Project, request.Lane, request.Branch)
	if err != nil {
		return place, nil, err
	}
	held, err := p.ownPlace(ctx, request, place)
	if err != nil {
		return lanes.Placement{}, nil, err
	}
	var kept *KeptWorktree
	if held != "" && !p.samePlace(ctx, held, place.Path) {
		kept = &KeptWorktree{Path: held, Would: place.Path}
	}
	if held != "" {
		place.Path = held
	}
	if p.Checkouts == nil {
		return place, kept, nil
	}
	clones, err := p.Checkouts.Every(ctx)
	if err != nil {
		return lanes.Placement{}, nil, err
	}
	clones = append(append(clones, p.Sources...), request.Repository)
	clone, inside, err := ports.InsideClone(ctx, p.Checkouts, clones, place.Path)
	if err != nil {
		return lanes.Placement{}, nil, err
	}
	switch {
	case inside && held != "":
		if kept == nil {
			kept = &KeptWorktree{Path: held, Would: place.Path}
		}
		kept.Inside = clone
	case inside:
		return lanes.Placement{}, nil, &WorktreeInsideClone{Root: path.Dir(place.Path), Clone: clone, Source: profile.Worktree.RootSource}
	}
	return place, kept, nil
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
