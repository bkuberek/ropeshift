package lanes

import (
	"fmt"
	"maps"
	"slices"
	"time"
)

// LaneLayerKeys are the settings a lane's own profile layer never sets, each as its path of TOML keys. Any
// caller of the API can send that layer, an outside delegate's included, and each of these would run a
// command as this machine's user, hand the lane a variable of the worker's environment, or put the
// worktree or its branch where the layers below did not.
func LaneLayerKeys() [][]string {
	return [][]string{{"hooks"}, {"agent", "credentials"}, {"worktree", "root"}, {"worktree", "branch_prefix"}}
}

// CheckLaneLayer holds a lane's own layer to the layers below it: below is the profile those layers give,
// and profile is the same with the lane's layer over it. The lane's layer changes none of LaneLayerKeys, it
// keeps every gate below, and it can lower a cap of the breaker and never raise one, because only a
// member's reset lets a lane past them (ADR-0014).
func CheckLaneLayer(below, profile Profile) error {
	if !maps.EqualFunc(below.Hooks, profile.Hooks, slices.Equal[[]string]) {
		return laneLayerRefusal("change [hooks]")
	}
	if gate, dropped := DroppedGate(below.Agent.Gates, profile.Agent.Gates); dropped {
		return laneLayerRefusal(fmt.Sprintf("drop the gate %q of agent.gates", gate))
	}
	if profile.Agent.Credentials != below.Agent.Credentials {
		return laneLayerRefusal("change agent.credentials")
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

// DroppedGate finds the first gate of below that gates no longer holds. A layer that sets agent.gates
// replaces the list of the layers below it, so a layer that leaves one out would let the action it gated
// run without asking.
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
