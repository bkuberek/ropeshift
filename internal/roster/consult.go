package roster

import "fmt"

// The refusals of a consultation's agent (FR-ORCH-9).
var (
	// ErrNotReadOnly means a consultation named an agent that writes, or one each start of needs a member's approval.
	ErrNotReadOnly = fmt.Errorf("%w: a consultation runs a read-only agent that needs no approval per use", ErrInvalid)
	// ErrNotStronger means no agent a consultation could run is of a class above the orchestrator's.
	ErrNotStronger = fmt.Errorf("%w: a consultation runs an agent of a class above the orchestrator's", ErrInvalid)
)

// ConsultAgent chooses the agent a consultation runs: the one named, or with none named the enabled read-only agent of
// the highest tier that needs no approval per use, the first of that tier in roster order. Either must be of a class
// above the orchestrator's, which above names, because a consultation asks an agent stronger than the one that consults.
func (r Resolved) ConsultAgent(named, above string) (ResolvedAgent, error) {
	if named != "" {
		agent, err := r.usable(named, "")
		if err != nil {
			return ResolvedAgent{}, err
		}
		if !consultable(agent.Agent) {
			return ResolvedAgent{}, fmt.Errorf("%w: agent %q is %s with approval %s", ErrNotReadOnly, named, agent.Access, agent.Approval)
		}
		if ClassRank(agent.Class) <= ClassRank(above) {
			return ResolvedAgent{}, fmt.Errorf("%w: agent %q is %s, and the orchestrator runs on %s", ErrNotStronger, named, agent.Class, above)
		}
		return agent, nil
	}
	var best *ResolvedAgent
	for i, agent := range r.Agents {
		if agent.Enabled && consultable(agent.Agent) && (best == nil || tier(agent.Agent) > tier(best.Agent)) {
			best = &r.Agents[i]
		}
	}
	if best == nil || ClassRank(best.Class) <= ClassRank(above) {
		return ResolvedAgent{}, fmt.Errorf("%w: no enabled read-only agent is above %s, the orchestrator's class", ErrNotStronger, above)
	}
	return *best, nil
}

// consultable says an agent reads and changes nothing, and runs with no member's approval of each start.
func consultable(agent Agent) bool {
	return agent.Access == AccessReadOnly && agent.Approval != ApprovalPerUse
}
