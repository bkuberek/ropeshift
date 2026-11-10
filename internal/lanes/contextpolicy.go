package lanes

// RoleWriter names a lane's thresholds in a profile. A role is an agent's function, and a lane
// is a writer unless its work order says otherwise.
const RoleWriter = "writer"

// RoleOrchestrator names an orchestrator's thresholds.
const RoleOrchestrator = "orchestrator"

// RolePlanner names a planning lane's thresholds, the lane that runs a `plan` work order.
const RolePlanner = "planner"

// RoleReviewer names a review lane's thresholds, the lane that runs a `review` work order.
const RoleReviewer = "reviewer"

// ContextPolicy is what one role's lane is held to (FR-LANE-8). The thresholds are absolute,
// because a fraction of a window would mean five times as much on one model as on another.
type ContextPolicy struct {
	RoleThresholds
	MinHeadroom int64
}

// ContextAct is what a lane does about its context, now.
type ContextAct string

// The acts of the context policy. Cheap ones run first, and a handoff is the most expensive.
const (
	// ContextWait does nothing yet.
	ContextWait ContextAct = ""
	// ContextCompact asks for a compaction, which runs only at rest.
	ContextCompact ContextAct = "compact"
	// ContextHandOff starts a handoff at rest.
	ContextHandOff ContextAct = "hand_off"
	// ContextInstruct starts a handoff during a turn. The instruction goes by whatever path reaches
	// a running turn, and the next tool call is denied until the session takes it, so a session with
	// no such path reads the same thing in the denial.
	ContextInstruct ContextAct = "instruct"
)

// ContextFacts is what one evaluation reads. Nothing here is a percentage of a window.
type ContextFacts struct {
	State ActualState
	// Tokens is the context of the last API call, and zero says no call has reported it: a status
	// line reads zero after a compaction until the next call.
	Tokens int64
	// Baseline is what this generation started with.
	Baseline int64
	// CanCompact says the driver has a path to a compaction.
	CanCompact bool
	// CompactPending says a compaction asked for waits on its next call's usage.
	CompactPending bool
	// CompactFailed says a compaction this generation left context over the threshold, or never ran.
	CompactFailed bool
}

// Decide reads one report. A handoff past its threshold comes first at rest, because past that
// figure a fresh start costs less than going on. Below it a compaction comes first. During a turn a
// compaction would only queue for the end of it, so the turn runs on until the handoff threshold,
// and a lane with no room for a handoff never gets one, or each generation would hand off at once.
func (p ContextPolicy) Decide(f ContextFacts) ContextAct {
	if f.CompactPending {
		return ContextWait
	}
	overCompact := p.CompactTokens > 0 && f.Tokens >= p.CompactTokens
	mayCompact := overCompact && f.CanCompact && !f.CompactFailed
	overHandoff := p.HandoffTokens > 0 && f.Tokens >= p.HandoffTokens
	handsOff := !p.Oversized(f.Baseline) && (overHandoff || (overCompact && !mayCompact))
	switch f.State {
	case Idle:
		switch {
		case handsOff:
			return ContextHandOff
		case mayCompact:
			return ContextCompact
		}
	case Working:
		if handsOff {
			return ContextInstruct
		}
	}
	return ContextWait
}

// Compacted reads the first call after a compaction, which is the only evidence it worked: the
// figure a compaction reports leaves out the system prefix, and a status line reads zero.
func (p ContextPolicy) Compacted(tokens int64) bool {
	return tokens > 0 && tokens < p.CompactTokens
}

// PolicyFor reads a role's policy from a profile.
func PolicyFor(settings ContextSettings, role string) ContextPolicy {
	return ContextPolicy{RoleThresholds: settings.ThresholdsFor(role), MinHeadroom: settings.MinHeadroomTokens}
}

// ThresholdsFor reads a role's thresholds from a profile. A planner or a reviewer the profile does not
// name takes the writer's, because a role with no thresholds would never compact and never hand off.
func (c ContextSettings) ThresholdsFor(role string) RoleThresholds {
	if thresholds, named := c.Roles[role]; named || (role != RolePlanner && role != RoleReviewer) {
		return thresholds
	}
	return c.Roles[RoleWriter]
}

// Oversized says what a generation starts with leaves too little room under its handoff threshold,
// so a new generation would only hand off again. It reads the measured baseline, and never a
// pack's estimate (D13).
func (p ContextPolicy) Oversized(baseline int64) bool {
	return baseline > 0 && p.HandoffTokens > 0 && p.HandoffTokens-baseline < p.MinHeadroom
}
