package questions

import "slices"

// The outcomes the core records on a question no live orchestrator took within the batch window (FR-QD-3).
const (
	// OutcomeUnjudged is a lookup rung the core ran by text: it lists what it found, and it judged none of it.
	OutcomeUnjudged = "unjudged"
	// OutcomeNoOrchestrator is a rung only an orchestrator works, which none did.
	OutcomeNoOrchestrator = "no_orchestrator"
)

// Unjudged takes a question no live orchestrator took within the batch window to a person (FR-QD-3). The core runs
// rungs 1 and 2 by text, so each lists what was found and judges nothing, and it records rungs 3 and 4 as no
// orchestrator's. Rungs an orchestrator already ran stay as it recorded them. The question keeps the options and the
// recommendation its lane gave, because nobody judged it, and it reaches a person for a one-way choice when the policy
// keeps one of its categories from an orchestrator, and as a scope change otherwise.
func Unjudged(q Question, p Policy, lookup, open []string) Question {
	steps := []Step{
		{Rung: RungLookup, Candidates: lookup, Outcome: OutcomeUnjudged},
		{Rung: RungOpen, Candidates: open, Outcome: OutcomeUnjudged},
		{Rung: RungDecide, Outcome: OutcomeNoOrchestrator},
		{Rung: RungAssume, Outcome: OutcomeNoOrchestrator},
	}
	for _, step := range steps {
		if !q.Ran(step.Rung) {
			q.LadderTrace = append(q.LadderTrace, step)
		}
	}
	q.EscalationReason = ReasonScopeChange
	if !p.MayDecide(q) {
		q.EscalationReason = ReasonOneWay
	}
	q.Status, q.Rung, q.ClaimedBy, q.ClaimUntil = StatusNeedsHuman, RungEscalate, "", 0
	return q
}

// NoOrchestrator says no orchestrator judged the question: the core took it to a person.
func (q Question) NoOrchestrator() bool {
	return slices.ContainsFunc(q.LadderTrace, func(step Step) bool { return step.Outcome == OutcomeNoOrchestrator })
}
