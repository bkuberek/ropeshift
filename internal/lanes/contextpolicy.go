package lanes

// RoleWriter names a lane's thresholds in a profile. A role is an agent's function, and phase 1
// starts every lane as a writer until planning and review lanes arrive.
const RoleWriter = "writer"

// ContextPolicy is what one role's lane is held to (FR-LANE-8). The thresholds are absolute,
// because a fraction of a window would mean five times as much on one model as on another.
type ContextPolicy struct {
	RoleThresholds
	MinHeadroom int64
}

// PolicyFor reads a role's policy from a profile.
func PolicyFor(settings ContextSettings, role string) ContextPolicy {
	return ContextPolicy{RoleThresholds: settings.Roles[role], MinHeadroom: settings.MinHeadroomTokens}
}

// Oversized says what a generation starts with leaves too little room under its handoff threshold,
// so a new generation would only hand off again. It reads the measured baseline, and never a
// pack's estimate (D13).
func (p ContextPolicy) Oversized(baseline int64) bool {
	return baseline > 0 && p.HandoffTokens > 0 && p.HandoffTokens-baseline < p.MinHeadroom
}
