package worker

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
)

// LaneRequest names a lane to prepare.
type LaneRequest struct {
	Lane       string
	Repository string
	// Project is the ID of the project the lane belongs to, which a worktree root's {project} names. Empty
	// names the installation's only project.
	Project string
	// PlanningRepository is the ID of the repository the project plans in, from the lease, which the project's
	// workspace is found from (FR-PROF-6). Empty when the project names none.
	PlanningRepository string
	// Recorded is the worktree the lane's journal recorded for an earlier generation. A lane goes on in its own
	// worktree when the root in force would put a new one elsewhere, and only the one recorded here, or when
	// nothing is recorded one named for the lane, is its own. Empty says the journal holds none.
	Recorded string
	// Branch is what the ledger assigned. Empty names a new branch for this lane.
	Branch string
	// Base is the commit a new branch begins at, which the control plane resolved from the base branch
	// when it granted the claim. A branch that exists keeps the commit it has. Empty begins a new
	// branch at the checkout's HEAD, which only a claim made before bases were resolved leaves.
	Base string
	// ProfileBase is the commit the repository's profile layer is read at: the lane's settings base, the commit its
	// work began from, and never a commit a lane wrote, such as the tip of a branch it continues or the commit a review
	// reads, so a layer a lane commits, and the hooks it names, reach no lane until it is merged. Empty reads the layer
	// at Base.
	ProfileBase string
	// ProfileUnbased says the lane has no commit to read the repository's profile layer at, as a review of a lane that
	// recorded no base has none, so its profile has no repository layer.
	ProfileUnbased bool
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
	// Delegated says a delegate sent Overrides, and under which delegate policy. A version that restricts it
	// keeps the layer from raising the model or the effort past what the lane would run without it
	// (FR-AUTH-16). Nil when a member, an orchestrator, or the system started the lane.
	Delegated *ledger.DelegatedLayer
	// Agent is the roster agent the lane runs as, whose model and effort the session runs unless Overrides
	// sets them. Nil runs as none.
	Agent *roster.Agent
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
	// Kept says the lane went on in a worktree it already had, at a place other than the one the root in force
	// gives a new worktree. Nil when the lane's worktree is where that root puts it, or is new.
	Kept *KeptWorktree
	// Fallback says why the lane's built-in root stayed the root outside every clone though its clone lies in a
	// workspace, and the start's notice says what to change. The zero value says nothing fell back.
	Fallback lanes.RootFallback
}

// KeptWorktree is a lane's existing worktree that the root in force would not have chosen, which an upgrade that
// moves the default root leaves behind for lanes in flight.
type KeptWorktree struct {
	// Path is the worktree the lane goes on in.
	Path string
	// Would is where the root in force puts a new worktree for the lane.
	Would string
	// Inside names the clone Path lies inside, which a new worktree there would be refused for. Empty when none.
	Inside string
}

// Provisioner gives a lane its settings, its worktree, and its environment. It holds
// no process, so a driver starts the session from what this returns.
type Provisioner struct {
	Profiles  ports.ProfileSource
	Worktrees ports.Worktrees
	// Hooks runs what a profile names. Nil refuses a profile that names a hook.
	Hooks ports.HookRunner
	// Checkouts lists every clone this worker keeps, so a worktree root inside one is refused (FR-PROF-3).
	// Nil checks nothing, for a test of what is not about the root.
	Checkouts ports.Checkouts
	// Sources are the absolute paths of the clones the daemon reads as sources, which no checkout records, so
	// the refusal checks them too. The repository a lane works in is always checked.
	Sources []string
	// Lookup reads the worker's environment. Nil reads the process's.
	Lookup func(string) (string, bool)
}

// ErrNoDetached means a lane asked for a worktree with no branch, and this worker's store makes only
// branches. A review would leave a branch behind.
var ErrNoDetached = errors.New("this worker's worktrees cannot check a commit out with no branch")

// laneSets says whether a lane's own profile layer changed the model or the effort from what the layers
// below it give, because that layer wins over the roster agent the lane runs as. It refuses a layer a
// delegate sent that raises either under a delegate policy that restricts it (FR-AUTH-16), against what the
// lane would run without the layer: its roster agent's model and effort, or for a lane that runs as no
// agent, the model the layers below give. A lane that runs as no agent is given no effort, whatever the
// layers say (asAgent), so it has none to raise and its effort is not compared. A lane whose session is
// given the profile's effort would have to compare it here.
func (p Provisioner) laneSets(ctx context.Context, request LaneRequest, profile lanes.Profile) (bool, bool, error) {
	if request.Overrides == "" {
		return false, false, nil
	}
	below, err := p.load(ctx, request, "")
	if err != nil {
		return false, false, err
	}
	laneModel, laneEffort := below.Agent.Model != profile.Agent.Model, below.Agent.Effort != profile.Agent.Effort
	was, runs := below.Agent, profile.Agent
	if request.Agent != nil {
		was.Model, was.Effort = agentRuns(below, *request.Agent, false, false)
		runs.Model, runs.Effort = agentRuns(profile, *request.Agent, laneModel, laneEffort)
	} else {
		was.Effort, runs.Effort = "", ""
	}
	if err := lanes.CheckDelegatedLayer(was, runs, request.Delegated); err != nil {
		return false, false, err
	}
	return laneModel, laneEffort, nil
}

// load resolves the lane's profile, reading its repository's layer at the commit the lane's work began from
// (ProfileBase), never at a commit a lane wrote. A lane with no work of its own has a directory and no repository,
// and a lane with no such commit (ProfileUnbased) reads none, so neither has a layer. A lane that another machine ran,
// and a review of a head another clone pushed, can name a commit this machine has yet to fetch, so a commit it does not
// hold is fetched for as the lane's worktree would and read once more.
func (p Provisioner) load(ctx context.Context, request LaneRequest, overrides string) (lanes.Profile, error) {
	repository, base := request.Repository, cmp.Or(request.ProfileBase, request.Base)
	if request.Private || request.ProfileUnbased {
		repository = ""
	}
	profile, err := p.Profiles.Load(ctx, repository, base, overrides)
	if !errors.Is(err, ports.ErrNotFound) || repository == "" {
		return profile, err
	}
	fetched := false
	if len(request.Fetch) != 0 {
		if err := p.Worktrees.Fetch(ctx, repository, request.Fetch); err != nil {
			return lanes.Profile{}, err
		}
		fetched = true
	}
	if trees, offered := p.Worktrees.(ports.ReviewTrees); request.Detached && offered && base != "" {
		if err := trees.FetchCommit(ctx, repository, base); err != nil {
			return lanes.Profile{}, err
		}
		fetched = true
	}
	if !fetched {
		return profile, err
	}
	return p.Profiles.Load(ctx, repository, base, overrides)
}

// Prepare resolves the profile first, because a bad profile should stop a lane
// before it leaves anything on disk, and a retry converges on the same worktree.
func (p Provisioner) Prepare(ctx context.Context, request LaneRequest) (PreparedLane, error) {
	if _, offered := p.Worktrees.(ports.ReviewTrees); request.Detached && !offered {
		return PreparedLane{}, ErrNoDetached
	}
	profile, err := p.load(ctx, request, request.Overrides)
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
	// The place comes before anything a lane leaves behind, so a root inside a clone stops it with no
	// resources allocated and no worktree made.
	var (
		place    lanes.Placement
		kept     *KeptWorktree
		fallback lanes.RootFallback
	)
	if !request.Private {
		if place, kept, fallback, err = p.place(ctx, request, profile); err != nil {
			return PreparedLane{}, err
		}
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
		LaneModel: laneModel, LaneEffort: laneEffort, Kept: kept, Fallback: fallback,
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
