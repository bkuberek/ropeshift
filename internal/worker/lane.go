package worker

import (
	"context"
	"os"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// LaneRequest names a lane to prepare.
type LaneRequest struct {
	Lane       string
	Repository string
	// Branch is what the ledger assigned. Empty names a new branch for this lane.
	Branch string
	Base   string
	// Tags are ropeshift's own variables for this lane.
	Tags map[string]string
	// Bracket records the worktree's creation as an external effect. It reports false
	// when an earlier attempt already made it. Nil creates it with no record.
	Bracket func(target string, perform func() error) (bool, error)
}

// PreparedLane is everything a lane starts from, and nothing it inherited.
type PreparedLane struct {
	Worktree      ports.Worktree
	Environment   []string
	Profile       lanes.Profile
	ProfileDigest string
	// DefaultBranch is the repository's, for the generation's policy. A policy guards the
	// usual names whether or not this read works, so a failed read stops nothing.
	DefaultBranch string
}

// Provisioner gives a lane its settings, its worktree, and its environment. It holds
// no process, so a driver starts the session from what this returns.
type Provisioner struct {
	Profiles  ports.ProfileSource
	Worktrees ports.Worktrees
	// Lookup reads the worker's environment. Nil reads the process's.
	Lookup func(string) (string, bool)
}

// Prepare resolves the profile first, because a bad profile should stop a lane
// before it leaves anything on disk, and a retry converges on the same worktree.
func (p Provisioner) Prepare(ctx context.Context, request LaneRequest) (PreparedLane, error) {
	profile, err := p.Profiles.Load(ctx, request.Repository)
	if err != nil {
		return PreparedLane{}, err
	}
	digest, err := profile.Digest()
	if err != nil {
		return PreparedLane{}, err
	}
	lookup := p.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	var refs []string
	if ref := profile.Agent.Credentials.SecretRef; ref != "" {
		refs = append(refs, ref)
	}
	environment, err := lanes.BuildEnvironment(lanes.EnvironmentSpec{Lookup: lookup, SecretRefs: refs, Tags: request.Tags})
	if err != nil {
		return PreparedLane{}, err
	}
	place, err := lanes.Place(profile.Worktree, request.Repository, request.Lane, request.Branch)
	if err != nil {
		return PreparedLane{}, err
	}
	worktree, err := p.worktree(ctx, request, place)
	if err != nil {
		return PreparedLane{}, err
	}
	branch, err := p.Worktrees.DefaultBranch(ctx, request.Repository)
	if err != nil {
		branch = ""
	}
	return PreparedLane{Worktree: worktree, Environment: environment, Profile: profile, ProfileDigest: digest, DefaultBranch: branch}, nil
}

func (p Provisioner) worktree(ctx context.Context, request LaneRequest, place lanes.Placement) (ports.Worktree, error) {
	create := func() (ports.Worktree, error) {
		return p.Worktrees.Create(ctx, ports.WorktreeSpec{
			Repository: request.Repository, Path: place.Path, Branch: place.Branch, Base: request.Base,
		})
	}
	if request.Bracket == nil {
		return create()
	}
	var worktree ports.Worktree
	ran, err := request.Bracket(place.Path, func() error {
		var err error
		worktree, err = create()
		return err
	})
	if err != nil || ran {
		return worktree, err
	}
	return p.Worktrees.Inspect(ctx, place.Path)
}
