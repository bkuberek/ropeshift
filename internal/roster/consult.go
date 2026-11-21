package roster

import "fmt"

// ConsultantAgent is the built-in agent a consultation runs when its call names none (FR-ORCH-9).
const ConsultantAgent = "consultant"

// The refusals of a consultation's agent (FR-ORCH-9).
var (
	// ErrNotReadOnly means a consultation named an agent that writes.
	ErrNotReadOnly = fmt.Errorf("%w: a consultation runs a read-only agent", ErrInvalid)
	// ErrNotStronger means no agent a consultation could run is of a class above the orchestrator's.
	ErrNotStronger = fmt.Errorf("%w: a consultation runs an agent of a class above the orchestrator's", ErrInvalid)
)

// ConsultAgent chooses the agent a consultation runs: the one named, or with none named the consultant. Either must be
// enabled, read-only, and of a class above the orchestrator's, which above names, because a consultation asks an agent
// stronger than the one that consults. The agent may need approval per use: a consultation is read-only, capped by
// `consultation_limit`, and one per question, so that limit is the standing approval of its use (FR-AGENT-11). A lane
// that runs a per-use agent still needs its approval.
func (r Resolved) ConsultAgent(named, above string) (ResolvedAgent, error) {
	if named == "" {
		named = ConsultantAgent
	}
	agent, err := r.usable(named, "")
	if err != nil {
		return ResolvedAgent{}, err
	}
	if agent.Access != AccessReadOnly {
		return ResolvedAgent{}, fmt.Errorf("%w: agent %q is %s", ErrNotReadOnly, named, agent.Access)
	}
	if ClassRank(agent.Class) <= ClassRank(above) {
		return ResolvedAgent{}, fmt.Errorf("%w: agent %q is %s, and the orchestrator runs on %s%s", ErrNotStronger, named, agent.Class, above, topClassRemedy(above))
	}
	return agent, nil
}

// topClassRemedy says what to change when the orchestrator runs on the top class, above which no agent of any roster can
// be, so a consultation is refused whichever agent it names, the default consultant included.
func topClassRemedy(above string) string {
	if ClassRank(above) != len(Classes)-1 {
		return ""
	}
	return fmt.Sprintf(": no class is above %s, so with orchestrator_class set to %s no consultation runs. Lower orchestrator_class below %s to allow one",
		above, above, above)
}
