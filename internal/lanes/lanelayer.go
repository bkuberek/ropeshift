package lanes

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/roster"
)

// LaneLayerKeys are the settings a route that takes a lane's own profile layer refuses in it, each as its
// path of TOML keys. Any caller of the API can send that layer, an outside delegate's included. These would
// move the lane to a driver whose classes map its roster agent to another model, run a command as this
// machine's user, hand the lane a variable of the worker's environment, name a file for the worker to read,
// or put the worktree or its branch where the layers below did not, and CheckLaneLayer refuses any change to
// them. A route holds none of the layers below, so it refuses each of these keys whenever a lane's layer sets
// it.
func LaneLayerKeys() [][]string {
	return [][]string{
		{"driver"}, {"hooks"}, {"agent", "credentials"}, {"agent", "instructions_file"}, {"worktree", "root"}, {"worktree", "branch_prefix"},
	}
}

// A LaneLayerFloor is a setting of a lane's own layer that CheckLaneLayer holds to the layers below it, and
// the one value of it no layers below can lower. A route holds none of those layers, so it cannot say
// whether a lane's layer raises the setting, but it can say the layer sets the floor, which no layers
// below can be under.
type LaneLayerFloor struct {
	Path  []string
	Value int64
}

// LaneLayerFloors are the settings of a lane's own layer that a route keeps only at their floor: one round
// of review, and no ports. The worker holds every other value to the layers below it, so a route refuses
// them.
func LaneLayerFloors() []LaneLayerFloor {
	return []LaneLayerFloor{
		{Path: []string{"review", "max_review_rounds"}, Value: 1},
		{Path: []string{"resources", "ports_per_lane"}, Value: 0},
	}
}

// CheckLaneLayer holds a lane's own layer to the layers below it: below is the profile those layers give,
// and profile is the same with the lane's layer over it. The lane's layer changes none of LaneLayerKeys, it
// keeps every gate below, it keeps or lowers the permission mode, it raises neither the bound on review rounds nor
// the ports per lane, and it can lower a cap of the breaker and never raise one, because only a member's reset lets
// a lane past them
// (ADR-0014). It holds every gate because it holds two profiles, however they were built: MergeLayers joins
// the layers' gates, so a profile resolved from layers always keeps them, and a lane's layer can only add.
func CheckLaneLayer(below, profile Profile) error {
	// The driver picks the model a roster agent's class maps to, so a lane that moved to another would change
	// what it runs without setting agent.model (FR-AUTH-16).
	if profile.Driver != below.Driver {
		return laneLayerRefusal("change driver")
	}
	if !maps.EqualFunc(below.Hooks, profile.Hooks, slices.Equal[[]string]) {
		return laneLayerRefusal("change [hooks]")
	}
	if gate, dropped := DroppedGate(below.Agent.Gates, profile.Agent.Gates); dropped {
		return laneLayerRefusal(fmt.Sprintf("drop the gate %q of agent.gates", gate))
	}
	// A mode the lane's layer leaves as it is raises nothing, one the layers below left unset included.
	if was, now := below.Agent.PermissionMode, profile.Agent.PermissionMode; now != was && RaisesPermission(was, now) {
		return laneLayerRefusal(fmt.Sprintf("raise agent.permission_mode from %q to %q", was, now))
	}
	if profile.Agent.Credentials != below.Agent.Credentials {
		return laneLayerRefusal("change agent.credentials")
	}
	// A file a profile names is one the worker would read, so only the layers below name it.
	if profile.Agent.InstructionsFile != below.Agent.InstructionsFile {
		return laneLayerRefusal("change agent.instructions_file")
	}
	if profile.Worktree.Root != below.Worktree.Root {
		return laneLayerRefusal("change worktree.root")
	}
	if profile.Worktree.BranchPrefix != below.Worktree.BranchPrefix {
		return laneLayerRefusal("change worktree.branch_prefix")
	}
	was, now := below.Lifecycle, profile.Lifecycle
	// Zero caps no day, so a lane that sets it where the layers below set a cap lifts that cap.
	if was.MaxGenerationsPerDay > 0 && (now.MaxGenerationsPerDay <= 0 || now.MaxGenerationsPerDay > was.MaxGenerationsPerDay) {
		return laneLayerRefusal(fmt.Sprintf("raise lifecycle.max_generations_per_day past %d", was.MaxGenerationsPerDay))
	}
	// Zero turns restarts off, which is the lowest a lane can ask for.
	if max(now.MaxRestarts, 0) > max(was.MaxRestarts, 0) {
		return laneLayerRefusal(fmt.Sprintf("raise lifecycle.max_restarts past %d", max(was.MaxRestarts, 0)))
	}
	if restartWindow(now.RestartWindow) < restartWindow(was.RestartWindow) {
		return laneLayerRefusal(fmt.Sprintf("shorten lifecycle.restart_window below %q", was.RestartWindow))
	}
	// A review lane reads its bound from its own profile, and an unset bound is the default, so the bound in
	// force is what compares.
	if profile.Review.Rounds() > below.Review.Rounds() {
		return laneLayerRefusal(fmt.Sprintf("raise review.max_review_rounds past %d", below.Review.Rounds()))
	}
	// Every lane on the worker draws its ports from one pool, and a lane that asks for none holds none.
	if ports := max(below.Resources.PortsPerLane, 0); max(profile.Resources.PortsPerLane, 0) > ports {
		return laneLayerRefusal(fmt.Sprintf("raise resources.ports_per_lane past %d", ports))
	}
	return nil
}

// CheckDelegatedLayer holds a lane's own layer that a delegate sent to the delegate policy in force when it did
// (FR-AUTH-16): was is the model and the effort the lane would run without that layer, with the user's
// `[agent.classes]`, and runs is what it runs with the layer. A lane that runs as a roster agent runs the
// agent's model and effort unless its layer sets them, so a layer that lowers the profile's model can still
// raise what the lane runs, and only what the lane runs compares. Under a version that restricts it, the layer
// raises neither the model nor the effort, and the refusal names the version. A layer no delegate sent, or one
// sent under a version that allows the raise, passes.
func CheckDelegatedLayer(was, runs AgentSettings, delegated *ledger.DelegatedLayer) error {
	if delegated == nil || !delegated.Restricted {
		return nil
	}
	if RaisesModel(was, runs) {
		return &DelegatePolicyRefusal{PolicyVersion: delegated.PolicyVersion, Setting: "agent.model", From: was.Model, To: runs.Model}
	}
	if RaisesEffort(was.Effort, runs.Effort) {
		return &DelegatePolicyRefusal{PolicyVersion: delegated.PolicyVersion, Setting: "agent.effort", From: was.Effort, To: runs.Effort}
	}
	return nil
}

// DelegatePolicyRefusal is a bad profile: a lane's own layer a delegate sent, which raises the setting from what
// the lane would run without it, under the version of the delegate policy that keeps it from doing so
// (FR-AUTH-16). The worker records it as the lane's reason, with its detail.
type DelegatePolicyRefusal struct {
	PolicyVersion     int64
	Setting, From, To string
}

// Error names the refusal as a bad profile.
func (e *DelegatePolicyRefusal) Error() string {
	return fmt.Sprintf("%v: %s", ErrBadProfile, e.Detail())
}

// Unwrap makes the refusal a bad profile.
func (e *DelegatePolicyRefusal) Unwrap() error { return ErrBadProfile }

// Detail names the version and what the layer raised, as a lane's outcome states it.
func (e *DelegatePolicyRefusal) Detail() string {
	return fmt.Sprintf("delegate policy version %d keeps a delegate's lane start from raising %s from %q to %q",
		e.PolicyVersion, e.Setting, e.From, e.To)
}

// RaisesModel says agent runs a model of a higher capability class than below's, by the user's
// `[agent.classes]`, which no lane's layer changes. When either model maps to no class, any other model is a
// raise. A model several classes map to counts at its highest class when it is the one a layer asks for and at
// its lowest when it is the one below, so a mapping that ranks it two ways never hides a raise.
func RaisesModel(below, agent AgentSettings) bool {
	if agent.Model == below.Model {
		return false
	}
	_, was := classRanks(below.Classes, below.Model)
	now, _ := classRanks(below.Classes, agent.Model)
	return was < 0 || now < 0 || now > was
}

// classRanks are the highest and the lowest rank of the classes that map to a model, or -1 for both when
// none does.
func classRanks(classes map[string]string, model string) (high, low int) {
	high, low = -1, -1
	for class, mapped := range classes {
		rank := roster.ClassRank(class)
		if mapped != model || rank < 0 {
			continue
		}
		if high < 0 || rank > high {
			high = rank
		}
		if low < 0 || rank < low {
			low = rank
		}
	}
	return high, low
}

// RaisesEffort says effort is higher than below on the product's scale of efforts, low, medium, high, and max.
// When either is no effort of the scale, any other effort is a raise, except that a lane that would run no
// effort runs the agent CLI's own, which is no lower than low: only low is no raise over it, and anything
// higher is.
func RaisesEffort(below, effort string) bool {
	if effort == below {
		return false
	}
	now := roster.EffortRank(effort)
	if below == "" {
		return now != roster.EffortRank(roster.EffortLow)
	}
	was := roster.EffortRank(below)
	return was < 0 || now < 0 || now > was
}

// DroppedGate finds the first gate of below that gates no longer holds. A profile that leaves one out would
// let the action it gated run without asking. MergeLayers makes each layer's gates join the layers below it,
// so no resolved profile drops one, and this stays as the check of a profile built another way.
func DroppedGate(below, gates []string) (string, bool) {
	for _, gate := range below {
		if !slices.Contains(gates, gate) {
			return gate, true
		}
	}
	return "", false
}

func laneLayerRefusal(what string) error {
	return fmt.Errorf("%w: a lane's own layer cannot %s", ErrBadProfile, what)
}

// restartWindow reads a window the way the breaker counts restarts in it. A window that does not parse, or
// is not positive, holds no restart, so it is the shortest, and lets a lane restart the most.
func restartWindow(text string) time.Duration {
	window, err := time.ParseDuration(text)
	if err != nil || window < 0 {
		return 0
	}
	return window
}
