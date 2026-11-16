package worker

import (
	"context"
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

// place says where a lane's worktree goes. A lane whose branch already stands in a worktree of its repository
// goes on in that worktree, wherever it is, because an upgrade can move the default root under a lane in flight
// and a second worktree for the same branch cannot be made. The root in force then applies only to a worktree not
// yet made, which is refused when it lies inside any clone this worker keeps, in any project, before anything
// exists there. A worktree that exists is never refused: the lane goes on in it and the second result says so.
func (p Provisioner) place(ctx context.Context, request LaneRequest, profile lanes.Profile) (lanes.Placement, *KeptWorktree, error) {
	place, err := lanes.Place(profile.Worktree, request.Repository, request.Project, request.Lane, request.Branch)
	if err != nil {
		return place, nil, err
	}
	held, err := p.heldPlace(ctx, request, place)
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

// heldPlace names the worktree the lane's branch is checked out in, or nothing. A review lane has no branch and
// a store that cannot say finds nothing.
func (p Provisioner) heldPlace(ctx context.Context, request LaneRequest, place lanes.Placement) (string, error) {
	trees, ok := p.Worktrees.(ports.BranchTrees)
	if !ok || request.Detached || place.Branch == "" {
		return "", nil
	}
	return trees.CheckedOut(ctx, request.Repository, place.Branch)
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
