package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// LaneRequest names a lane to prepare.
type LaneRequest struct {
	Lane       string
	Repository string
	// Branch is what the ledger assigned. Empty names a new branch for this lane.
	Branch string
	// Base is the commit a new branch begins at, which the control plane resolved from the base branch
	// when it granted the claim. A branch that exists keeps the commit it has. Empty begins a new
	// branch at the checkout's HEAD, which only a claim made before bases were resolved leaves.
	Base string
	// Tags are ropeshift's own variables for this lane.
	Tags map[string]string
	// Bracket records the worktree's creation as an external effect. It reports false
	// when an earlier attempt already made it. Nil creates it with no record.
	Bracket func(target string, perform func() error) (bool, error)
	// Allocate gives the lane its resources. Nil gives it none.
	Allocate func(lanes.ResourceSettings) (lanes.Allocation, error)
	// RunHook runs a profile's hook once, as an external effect. Nil runs it with no
	// record.
	RunHook func(target string, perform func() error) error
	// Overrides is this lane's own profile layer.
	Overrides string
	// Fetch names what a remote holds and this machine must bring before a worktree exists,
	// for a lane another machine ran.
	Fetch []string
	// Detached makes the worktree a checkout of Base with no branch, for a lane that reads and leaves
	// nothing behind. Base is then the commit to read.
	Detached bool
	// Private says this session has no work: Repository is a directory of its own, with no
	// worktree in it, no ports, and no hook of a repository's to run.
	Private bool
	// ForgeConfigDir is where the session's `gh` reads its configuration. Prepare empties it first, so
	// the person's sign-in is never there for an agent to use (ADR-0020). Empty sets nothing.
	ForgeConfigDir string
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
	// LaneModel and LaneEffort say the lane's own profile layer set the model or the effort, which then
	// wins over the roster agent the lane runs as (ADR-0025).
	LaneModel, LaneEffort bool
}

// Provisioner gives a lane its settings, its worktree, and its environment. It holds
// no process, so a driver starts the session from what this returns.
type Provisioner struct {
	Profiles  ports.ProfileSource
	Worktrees ports.Worktrees
	// Hooks runs what a profile names. Nil refuses a profile that names a hook.
	Hooks ports.HookRunner
	// Lookup reads the worker's environment. Nil reads the process's.
	Lookup func(string) (string, bool)
}

// ErrNoDetached means a lane asked for a worktree with no branch, and this worker's store makes only
// branches. A review would leave a branch behind.
var ErrNoDetached = errors.New("this worker's worktrees cannot check a commit out with no branch")

// laneSets says whether a lane's own profile layer changed the model or the effort from what the layers
// below it give, because that layer wins over the roster agent the lane runs as.
func (p Provisioner) laneSets(ctx context.Context, request LaneRequest, profile lanes.Profile) (bool, bool, error) {
	if request.Overrides == "" {
		return false, false, nil
	}
	below, err := p.Profiles.Load(ctx, request.Repository, "")
	if err != nil {
		return false, false, err
	}
	return below.Agent.Model != profile.Agent.Model, below.Agent.Effort != profile.Agent.Effort, nil
}

// Prepare resolves the profile first, because a bad profile should stop a lane
// before it leaves anything on disk, and a retry converges on the same worktree.
func (p Provisioner) Prepare(ctx context.Context, request LaneRequest) (PreparedLane, error) {
	if _, offered := p.Worktrees.(ports.ReviewTrees); request.Detached && !offered {
		return PreparedLane{}, ErrNoDetached
	}
	profile, err := p.Profiles.Load(ctx, request.Repository, request.Overrides)
	if err != nil {
		return PreparedLane{}, err
	}
	laneModel, laneEffort, err := p.laneSets(ctx, request, profile)
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
	var allocation lanes.Allocation
	if request.Allocate != nil && !request.Private {
		if allocation, err = request.Allocate(profile.Resources); err != nil {
			return PreparedLane{}, err
		}
	}
	if err := emptyDir(request.ForgeConfigDir); err != nil {
		return PreparedLane{}, err
	}
	environment, err := lanes.BuildEnvironment(lanes.EnvironmentSpec{
		Lookup: lookup, SecretRefs: refs, Tags: request.Tags, Allocation: allocation, ForgeConfigDir: request.ForgeConfigDir,
	})
	if err != nil {
		return PreparedLane{}, err
	}
	if request.Private {
		return PreparedLane{
			Worktree: ports.Worktree{Path: request.Repository}, Environment: environment, Profile: profile, ProfileDigest: digest,
			LaneModel: laneModel, LaneEffort: laneEffort,
		}, nil
	}
	place, err := lanes.Place(profile.Worktree, request.Repository, request.Lane, request.Branch)
	if err != nil {
		return PreparedLane{}, err
	}
	worktree, err := p.worktree(ctx, request, place)
	if err != nil {
		return PreparedLane{}, err
	}
	if err := p.laneCreated(ctx, request, profile, worktree, environment); err != nil {
		return PreparedLane{}, err
	}
	branch, err := p.Worktrees.DefaultBranch(ctx, request.Repository)
	if err != nil {
		branch = ""
	}
	return PreparedLane{
		Worktree: worktree, Environment: environment, Profile: profile, ProfileDigest: digest, DefaultBranch: branch,
		LaneModel: laneModel, LaneEffort: laneEffort,
	}, nil
}

// emptyDir leaves a directory that exists and holds nothing, whatever an earlier generation wrote there.
func emptyDir(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o700)
}

// hookTimeout bounds a lane's setup, which a stuck script must not hold forever.
const hookTimeout = 10 * time.Minute

// ErrHookFailed means a profile's hook ended badly, so no session starts on what it left.
var ErrHookFailed = errors.New("a profile's hook failed")

// laneCreated runs what a profile names for a new lane, in its worktree. The hook gets the
// lane's environment without its token, because a repository's script holds no authority
// of the lane's.
func (p Provisioner) laneCreated(ctx context.Context, request LaneRequest, profile lanes.Profile, worktree ports.Worktree, environment []string) error {
	argv := profile.Hooks[lanes.HookLaneCreated]
	if len(argv) == 0 {
		return nil
	}
	if p.Hooks == nil {
		return fmt.Errorf("%w: this worker runs no hooks", ErrHookFailed)
	}
	perform := func() error {
		result, err := p.Hooks.Run(ctx, ports.HookCommand{
			Argv: lanes.ExpandHook(argv, request.Lane), Dir: worktree.Path, Env: withoutToken(environment), Timeout: hookTimeout,
		})
		if err != nil {
			return fmt.Errorf("%w: %v", ErrHookFailed, err)
		}
		if result.Exit.Code != 0 || result.Exit.Signal != "" {
			return fmt.Errorf("%w: exit %+v: %s", ErrHookFailed, result.Exit, bytes.TrimSpace(result.Output))
		}
		return nil
	}
	if request.RunHook == nil {
		return perform()
	}
	return request.RunHook(worktree.Path, perform)
}

func withoutToken(environment []string) []string {
	out := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, lanes.EnvLaneToken+"=") {
			out = append(out, entry)
		}
	}
	return out
}

func (p Provisioner) worktree(ctx context.Context, request LaneRequest, place lanes.Placement) (ports.Worktree, error) {
	create := func() (ports.Worktree, error) {
		if len(request.Fetch) != 0 {
			if err := p.Worktrees.Fetch(ctx, request.Repository, request.Fetch); err != nil {
				return ports.Worktree{}, err
			}
		}
		return p.Worktrees.Create(ctx, ports.WorktreeSpec{
			Repository: request.Repository, Path: place.Path, Branch: place.Branch, Base: request.Base, Detached: request.Detached,
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
