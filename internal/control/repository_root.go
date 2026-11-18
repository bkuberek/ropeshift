package control

import (
	"context"
	"fmt"
	"path"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
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
	profile, err := r.profiles.Load(ctx, checkout, "", "")
	if err != nil {
		return "", nil
	}
	// The project's workspace counts as it does for a lane's start: found from the user's profile or from the
	// planning clone the project's records name, it moves a built-in root into its folder for worktrees, and it
	// allows a root under that folder while it ignores it.
	planned, err := r.planningRepository(ctx, scope)
	if err != nil {
		return "", err
	}
	workspaces := ports.NewWorkspace(kept)
	workspace, found, err := workspaces.Find(ctx, profile, string(scope), planned)
	if err != nil {
		return "", err
	}
	settings, allowed := profile.Worktree, ""
	if found {
		if settings, _, err = workspaces.Settings(ctx, settings, workspace, checkout, rootProbe); err != nil {
			return "", err
		}
		if workspace.Clone {
			allowed = workspace.Path
		}
	}
	place, err := lanes.Place(settings, checkout, string(scope), rootProbe, "")
	if err != nil {
		return "", nil
	}
	_, inside, err := ports.InsideClone(ctx, kept, []string{checkout}, place.Path, allowed)
	if err != nil || !inside {
		return "", err
	}
	fix := "set `[worktree] root` in your profile to a folder outside every clone, or remove it to use the built-in root"
	if found && workspace.Clone && ports.SamePath(ctx, kept, checkout, workspace.Path) {
		under, err := ports.UnderWorkspaceWorktrees(ctx, kept, workspace.Path, place.Path)
		if err != nil {
			return "", err
		}
		if under {
			fix = fmt.Sprintf("add `%s` to the .gitignore of %s, or %s", planning.WorktreesLine, workspace.Path, fix)
		}
	}
	return fmt.Sprintf("worktree root %s, set by %s, is inside this clone, so a lane here would fail with the reason %s: %s",
		path.Dir(place.Path), profile.Worktree.RootSource, workorders.ReasonWorktreeInsideClone, fix), nil
}

// planningRepository reads the ID of the repository the project's records name its planning repository, which is
// where the project's workspace is found from, or nothing when it names none.
func (r *Repositories) planningRepository(ctx context.Context, scope ledger.WorkspaceID) (string, error) {
	var id string
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		id, err = planningRepository(tx.Repositories())
		return err
	})
	return id, err
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
	added.Reason = noted(added.Reason, note)
	return added, nil
}
