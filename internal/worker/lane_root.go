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

// place says where a lane's worktree goes, and refuses a place inside any clone this worker keeps, in any
// project, before anything exists there.
func (p Provisioner) place(ctx context.Context, request LaneRequest, profile lanes.Profile) (lanes.Placement, error) {
	place, err := lanes.Place(profile.Worktree, request.Repository, request.Project, request.Lane, request.Branch)
	if err != nil || p.Checkouts == nil {
		return place, err
	}
	clones, err := p.Checkouts.Every(ctx)
	if err != nil {
		return lanes.Placement{}, err
	}
	clone, inside, err := ports.InsideClone(ctx, p.Checkouts, clones, place.Path)
	if err != nil {
		return lanes.Placement{}, err
	}
	if inside {
		return lanes.Placement{}, &WorktreeInsideClone{Root: path.Dir(place.Path), Clone: clone, Source: profile.Worktree.RootSource}
	}
	return place, nil
}
