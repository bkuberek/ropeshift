package ledger

// SignInUsage is what a worker knows of its sign-in's usage limit, which it reports with its resources, so a member
// reads why its orchestrator does not run and when it could. ResetsAtMs is the vendor's reset, in epoch milliseconds,
// and zero when nobody named one. Only durations cross the wire otherwise, so ReadAgeMs is how long before the report
// the worker last heard of the limit, which the control plane dates by its own clock.
type SignInUsage struct {
	Limited    bool    `json:"limited,omitempty"`
	Window     string  `json:"window,omitempty"`
	ResetsAtMs int64   `json:"resets_at_ms,omitempty"`
	Used       float64 `json:"used_percentage,omitempty"`
	UsedKnown  bool    `json:"used_known,omitempty"`
	ReadAgeMs  int64   `json:"read_age_ms,omitempty"`
}

// The windows a vendor names, as architecture's usage record holds them: the five-hour session and the week.
const (
	UsageWindowSession = "five_hour"
	UsageWindowWeekly  = "seven_day"
)

// maxUsageReadAgeMs bounds how long before its report a worker says it heard of its limit: the longest window, past
// which any reset it heard is stale whatever its age.
const maxUsageReadAgeMs = 7 * 24 * 60 * 60 * 1000

// usageWindowMs is how long a window lasts, which is how long a reset read in it can be trusted. A window nobody named
// is taken as the session's, the shorter.
func usageWindowMs(window string) int64 {
	if window == UsageWindowWeekly {
		return 7 * 24 * 60 * 60 * 1000
	}
	return 5 * 60 * 60 * 1000
}

// Why an orchestrator cannot start or run now.
const (
	HoldOffline    = "offline"
	HoldTripped    = "tripped"
	HoldUsageLimit = "usage_limit"
)

// OrchestratorHold is why a worker's orchestrator cannot start or run now. For a usage limit, Window names it, ResetsAt
// is when it resets, zero when nobody said, ReadAt when the worker heard it, by the control
// plane's clock, and Stale says the reset is too old to trust: it passed with the limit still holding, or it was read
// longer ago than its window lasts.
type OrchestratorHold struct {
	Reason   string `json:"reason"`
	Window   string `json:"window,omitempty"`
	ResetsAt int64  `json:"resets_at,omitempty"`
	ReadAt   int64  `json:"read_at,omitempty"`
	Stale    bool   `json:"stale,omitempty"`
}

// OrchestratorHoldOf says what keeps a worker's orchestrator from starting or running now, most basic first: a worker that is not
// connected starts nothing, a breaker that tripped holds until a person resets it, and a sign-in at its limit holds
// until its reset. Nil says nothing does. readAt is when the worker heard of the limit, by the control plane's clock.
func OrchestratorHoldOf(connected bool, row *Orchestrator, usage *SignInUsage, readAt, now int64) *OrchestratorHold {
	switch {
	case !connected:
		return &OrchestratorHold{Reason: HoldOffline}
	case row != nil && row.Desired == OrchestratorRunning && row.Tripped():
		return &OrchestratorHold{Reason: HoldTripped}
	case usage == nil || !usage.Limited:
		return nil
	}
	hold := &OrchestratorHold{Reason: HoldUsageLimit, Window: usage.Window, ResetsAt: usage.ResetsAtMs, ReadAt: readAt}
	hold.Stale = hold.ResetsAt != 0 && (now >= hold.ResetsAt || now-readAt > usageWindowMs(usage.Window))
	return hold
}

// StartStep is what a scheduled start does when its tick reads it.
type StartStep string

// The steps of a scheduled start.
const (
	StartWaits StartStep = "waits"
	StartMoves StartStep = "moves"
	StartFires StartStep = "fires"
	StartEnds  StartStep = "ends"
)

// Step says what a scheduled start does now, and what it becomes, from the worker's last word on its sign-in and when
// the control plane heard it. It does nothing for an orchestrator already asked to run, and starts the orchestrator
// once the limit is gone, before its reset or after. A report that says nothing of the sign-in, as a worker of an
// earlier build sends, says nothing of the limit either, so the start waits. While its reset is ahead it waits, or moves to a reset reported since; once its reset came it waits
// for the worker's first word since, and when that word says the limit still holds, the reset was stale: it moves to
// the reset reported since, or waits for the next one reported, and Missed counts it.
func (s ScheduledStart) Step(usage *SignInUsage, reportedAt int64, runs bool, now int64) (StartStep, ScheduledStart) {
	came := s.StartsAt != 0 && now >= s.StartsAt
	switch {
	case runs:
		return StartEnds, s
	case usage == nil:
		return StartWaits, s
	case came && reportedAt < s.StartsAt:
		return StartWaits, s
	case !usage.Limited:
		return StartFires, s
	}
	next := s
	if came {
		next.Missed++
		next.StartsAt = 0
	}
	if usage.ResetsAtMs > now && usage.ResetsAtMs != s.StartsAt {
		next.StartsAt = usage.ResetsAtMs
	}
	if next == s {
		return StartWaits, s
	}
	return StartMoves, next
}
