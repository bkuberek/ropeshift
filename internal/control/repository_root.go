package control

import (
	"context"
	"fmt"
	"path"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Rooted makes every registration read the worktree root in force for a clone, from the profile source, and
// say in the outcome's reason when that root would lie inside the clone (FR-PROF-3, FR-REPO-15). Without a
// source a registration says nothing of the root.
func (r *Repositories) Rooted(profiles ports.ProfileSource) *Repositories {
	r.profiles = profiles
	return r
}

// rootProbe names the lane a clone's worktree root is read for. Any name does: the root holds no lane.
const rootProbe = "lane"

// rootNote says, for the clone kept at checkout in a project, that its profile's worktree root would put
// every lane's worktree inside it, or nothing when the root lies outside it or no profile is read. The
// clone is registered all the same: the root is a setting a member changes, and a lane that starts under it
// fails with the reason `worktree_inside_clone`. A profile that does not load says nothing here, because a
// lane's start names it.
func (r *Repositories) rootNote(ctx context.Context, scope ledger.WorkspaceID, kept ports.Checkouts, checkout string) (string, error) {
	if r.profiles == nil || checkout == "" {
		return "", nil
	}
	profile, err := r.profiles.Load(ctx, checkout, "")
	if err != nil {
		return "", nil
	}
	place, err := lanes.Place(profile.Worktree, checkout, string(scope), rootProbe, "")
	if err != nil {
		return "", nil
	}
	_, inside, err := ports.InsideClone(ctx, kept, []string{checkout}, place.Path)
	if err != nil || !inside {
		return "", err
	}
	return fmt.Sprintf("worktree root %s, set by %s, is inside this clone, so a lane here would fail with the reason %s: set `[worktree] root` in your profile to a folder outside every clone, or remove it to use the built-in root",
		path.Dir(place.Path), profile.Worktree.RootSource, workorders.ReasonWorktreeInsideClone), nil
}

// withRootNote adds the clone's root note to a registration's outcome. A registration that was refused is
// left as it is.
func (r *Repositories) withRootNote(ctx context.Context, scope ledger.WorkspaceID, kept ports.Checkouts, added Added, checkout string) (Added, error) {
	if added.Outcome == ledger.RegistrationRefused {
		return added, nil
	}
	note, err := r.rootNote(ctx, scope, kept, checkout)
	if err != nil || note == "" {
		return added, err
	}
	if added.Reason == "" {
		added.Reason = note
	} else {
		added.Reason = strings.Join([]string{added.Reason, note}, "; ")
	}
	return added, nil
}
