package roster

import (
	"cmp"
	"fmt"
	"slices"
)

// The work order kinds the default mapping reads. They are the work orders' own, named here so this
// package stands alone.
const (
	workPlan           = "plan"
	workContractReview = "contract_review"
	workChange         = "change"
	workRework         = "rework"
	workReview         = "review"
)

// LaneWork is what chooses a lane's agent.
type LaneWork struct {
	Kind string
	Size string
	// Named is the agent the work order names, which wins.
	Named string
	// Reworks is the agent that ran the work this lane reworks: the work a `rework` work order reworks,
	// or the attempt a review or a member sent back. Empty when this lane reworks nothing.
	Reworks string
}

// LaneAgent chooses the agent a lane runs as (FR-AGENT-13): the agent its work order names, or the
// default mapping's by kind and size, where rework moves one implementation tier up and never into a
// per-use agent. An unknown or disabled agent is refused by name.
func (r Resolved) LaneAgent(work LaneWork) (ResolvedAgent, error) {
	name := work.Named
	if name == "" {
		name = r.defaultFor(work)
	}
	return r.usable(name)
}

// DefaultAgent is the agent the default mapping alone gives a work order, before any rework, which is
// what the definition of ready reads.
func (r Resolved) DefaultAgent(kind, size, named string) (ResolvedAgent, error) {
	return r.LaneAgent(LaneWork{Kind: kind, Size: size, Named: named})
}

func (r Resolved) usable(name string) (ResolvedAgent, error) {
	agent, ok := r.Agent(name)
	switch {
	case name == "":
		return ResolvedAgent{}, fmt.Errorf("%w: the default mapping names no agent for this work", ErrUnknownAgent)
	case !ok:
		return ResolvedAgent{}, fmt.Errorf("%w: the resolved roster holds no agent %q", ErrUnknownAgent, name)
	case !agent.Enabled:
		return ResolvedAgent{}, fmt.Errorf("%w: agent %q is disabled in the resolved roster", ErrDisabledAgent, name)
	}
	return agent, nil
}

func (r Resolved) defaultFor(work LaneWork) string {
	switch work.Kind {
	case workPlan:
		return r.Defaults.Plan
	case workContractReview:
		return r.Defaults.ContractReview
	case workReview:
		return r.Defaults.Review
	}
	byDefault := r.Defaults.Change[sizeOf(work.Size)]
	if work.Kind == workRework && work.Reworks == "" {
		return r.nextTier(byDefault)
	}
	if work.Reworks != "" && (work.Kind == workChange || work.Kind == workRework) {
		return r.nextTier(work.Reworks)
	}
	return byDefault
}

// sizeOf counts an absent or unknown size as medium.
func sizeOf(size string) string {
	if size == SizeSmall || size == SizeLarge {
		return size
	}
	return SizeMedium
}

// nextTier is the implementation agent one tier above from: the first enabled implementation agent
// with no per-use approval whose class and effort rank above it. With none above, from stays when it
// is usable, and otherwise the highest such agent takes the work.
func (r Resolved) nextTier(from string) string {
	ladder := r.ladder()
	if len(ladder) == 0 {
		return from
	}
	held, known := r.Agent(from)
	if known {
		for _, agent := range ladder {
			if tier(agent.Agent) > tier(held.Agent) {
				return agent.Name
			}
		}
		if held.Enabled {
			return from
		}
	}
	return ladder[len(ladder)-1].Name
}

// ladder lists the implementation agents rework may move into, from the lowest tier.
func (r Resolved) ladder() []ResolvedAgent {
	var out []ResolvedAgent
	for _, agent := range r.Agents {
		if agent.Kind == KindImplementation && agent.Enabled && agent.Approval != ApprovalPerUse {
			out = append(out, agent)
		}
	}
	slices.SortStableFunc(out, func(a, b ResolvedAgent) int {
		return cmp.Or(cmp.Compare(tier(a.Agent), tier(b.Agent)), cmp.Compare(a.Name, b.Name))
	})
	return out
}

// tier ranks an agent by its class, then its effort.
func tier(a Agent) int { return rank(Classes, a.Class)*len(efforts) + rank(efforts, a.Effort) }

// Use is something that names an agent: the default mapping, or a work order that is not done.
type Use struct {
	Agent string `json:"agent"`
	By    string `json:"by"`
}

// DefaultUses lists what the default mapping names.
func (r Resolved) DefaultUses() []Use { return r.Defaults.names() }

// Missing lists each use whose agent this roster does not hold enabled. A change whose result has any
// is refused, because what names the agent would fail the definition of ready.
func (r Resolved) Missing(uses []Use) []Use {
	var out []Use
	for _, use := range uses {
		if _, err := r.usable(use.Agent); err != nil {
			out = append(out, use)
		}
	}
	return out
}
