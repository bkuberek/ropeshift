package lanes

import "time"

// ActualState is what a lane is doing. Only what happened moves it: driver events, hooks,
// and the worker's own acts. No person and no agent can set it (FR-LANE-5). Record.State
// is coarser: it says whether a restart still owes this lane an end.
type ActualState string

// The states of a lane.
const (
	Pending       ActualState = "pending"
	Provisioning  ActualState = "provisioning"
	Starting      ActualState = "starting"
	Briefing      ActualState = "briefing"
	Working       ActualState = "working"
	Idle          ActualState = "idle"
	NeedsDecision ActualState = "needs_decision"
	Compacting    ActualState = "compacting"
	HandingOff    ActualState = "handing_off"
	Parking       ActualState = "parking"
	Parked        ActualState = "parked"
	Retiring      ActualState = "retiring"
	Backoff       ActualState = "backoff"
	Tripped       ActualState = "tripped"
	Ended         ActualState = "ended"
)

// ActualStates lists every state, in the design's order.
var ActualStates = []ActualState{
	Pending, Provisioning, Starting, Briefing, Working, Idle, NeedsDecision, Compacting,
	HandingOff, Parking, Parked, Retiring, Backoff, Tripped, Ended,
}

// Signal is one thing that happened to a lane.
type Signal string

// The signals. Each is an observed fact or a worker's own act, and none is a person
// saying a lane is ready, so no API can reach one.
const (
	OnClaimGranted      Signal = "claim_granted"
	OnResourcesReady    Signal = "resources_ready"
	OnDriverReady       Signal = "driver_ready"
	OnResumeNotReady    Signal = "resume_not_ready"
	OnUnknownScreen     Signal = "unknown_screen"
	OnActivity          Signal = "activity"
	OnBriefGrace        Signal = "brief_grace_elapsed"
	OnTurnFinished      Signal = "turn_finished"
	OnInputHanded       Signal = "input_handed"
	OnDecisionPending   Signal = "decision_pending"
	OnDecisionReturned  Signal = "decision_returned"
	OnWaitBound         Signal = "wait_bound_reached"
	OnCompactionStarted Signal = "compaction_started"
	// OnCompactionFinished is the fact a hook reports. A Tracker turns it into one of the
	// two below, from the state the compaction left.
	OnCompactionFinished Signal = "compaction_finished"
	OnFinishedInTurn     Signal = "compaction_finished_in_turn"
	OnFinishedAtRest     Signal = "compaction_finished_at_rest"
	// OnCompactionFailed says a session went on from a compaction that never finished. No hook
	// reports a failure, so this is what a tool call or a turn's end shows.
	OnCompactionFailed Signal = "compaction_failed"
	OnHandoffThreshold Signal = "handoff_threshold"
	OnHandoffWritten   Signal = "handoff_written"
	OnNextGeneration   Signal = "next_generation"
	OnCannotProceed    Signal = "cannot_proceed"
	OnParkRequested    Signal = "park_requested"
	OnProcessGone      Signal = "process_gone"
	OnWakeFired        Signal = "wake_fired"
	OnSessionGone      Signal = "session_gone"
	OnDelayElapsed     Signal = "delay_elapsed"
	OnRestartLimit     Signal = "restart_limit"
	OnHumanReset       Signal = "human_reset"
	OnOutcomeReported  Signal = "outcome_reported"
	OnStopRequested    Signal = "stop_requested"
	OnSessionStopped   Signal = "session_stopped"
	// OnLaneEnding covers a lane with no next generation from a state the diagram gives
	// no way out of: a start that failed, a session gone with no restart policy, or a
	// stop from anywhere but `working`.
	OnLaneEnding Signal = "lane_ending"
)

// Signals lists every signal.
var Signals = []Signal{
	OnClaimGranted, OnResourcesReady, OnDriverReady, OnResumeNotReady, OnUnknownScreen, OnActivity,
	OnBriefGrace, OnTurnFinished, OnInputHanded, OnDecisionPending, OnDecisionReturned, OnWaitBound,
	OnCompactionStarted, OnCompactionFinished, OnFinishedInTurn, OnFinishedAtRest, OnCompactionFailed, OnHandoffThreshold,
	OnHandoffWritten, OnNextGeneration, OnCannotProceed, OnParkRequested, OnProcessGone, OnWakeFired,
	OnSessionGone, OnDelayElapsed, OnRestartLimit, OnHumanReset, OnOutcomeReported, OnStopRequested,
	OnSessionStopped, OnLaneEnding,
}

type edge struct {
	from ActualState
	on   Signal
}

// transitions is the lane state diagram.
var transitions = map[edge]ActualState{
	{Pending, OnClaimGranted}:           Provisioning,
	{Provisioning, OnResourcesReady}:    Starting,
	{Starting, OnDriverReady}:           Briefing,
	{Starting, OnResumeNotReady}:        Retiring,
	{Starting, OnUnknownScreen}:         Parking,
	{Briefing, OnActivity}:              Working,
	{Briefing, OnBriefGrace}:            Idle,
	{Working, OnTurnFinished}:           Idle,
	{Idle, OnInputHanded}:               Working,
	{Working, OnDecisionPending}:        NeedsDecision,
	{NeedsDecision, OnDecisionReturned}: Working,
	{NeedsDecision, OnWaitBound}:        Parking,
	{Working, OnCompactionStarted}:      Compacting,
	{Idle, OnCompactionStarted}:         Compacting,
	{Compacting, OnFinishedInTurn}:      Working,
	{Compacting, OnFinishedAtRest}:      Idle,
	{Compacting, OnCompactionFailed}:    Working,
	{Working, OnHandoffThreshold}:       HandingOff,
	{Idle, OnHandoffThreshold}:          HandingOff,
	{HandingOff, OnHandoffWritten}:      Retiring,
	{Retiring, OnNextGeneration}:        Starting,
	{Working, OnCannotProceed}:          Parking,
	{Idle, OnParkRequested}:             Parking,
	{Parking, OnProcessGone}:            Parked,
	{Parked, OnWakeFired}:               Provisioning,
	{Working, OnSessionGone}:            Backoff,
	{Idle, OnSessionGone}:               Backoff,
	{NeedsDecision, OnSessionGone}:      Backoff,
	{Compacting, OnSessionGone}:         Backoff,
	{Backoff, OnDelayElapsed}:           Starting,
	{Backoff, OnRestartLimit}:           Tripped,
	{Retiring, OnRestartLimit}:          Tripped,
	{Parked, OnRestartLimit}:            Tripped,
	{Tripped, OnHumanReset}:             Starting,
	{Idle, OnOutcomeReported}:           Retiring,
	{Working, OnStopRequested}:          Retiring,
	{Retiring, OnSessionStopped}:        Ended,
}

// endingStates are where OnLaneEnding reaches, because the diagram has no way from them
// to `retiring`, and a lane must still end.
var endingStates = []ActualState{
	Pending, Provisioning, Starting, Briefing, Working, Idle, NeedsDecision, Compacting,
	HandingOff, Parking, Backoff, Tripped,
}

func init() {
	for _, state := range endingStates {
		transitions[edge{state, OnLaneEnding}] = Retiring
	}
}

// Next returns where a signal takes a lane. A signal with no edge from this state moves
// nothing, because an event can arrive late or twice, and that is not an error.
func Next(state ActualState, signal Signal) (ActualState, bool) {
	next, ok := transitions[edge{state, signal}]
	if !ok {
		return state, false
	}
	return next, true
}

// Tracker follows one lane through the table.
type Tracker struct {
	state ActualState
	// resume is the state a compaction left.
	resume ActualState
}

// NewTracker starts from a state a journal kept.
func NewTracker(state ActualState) *Tracker { return &Tracker{state: state} }

// ResumeTracker starts from a state a journal kept, and the state a compaction under way left.
func ResumeTracker(state, from ActualState) *Tracker { return &Tracker{state: state, resume: from} }

// State returns where the lane is.
func (t *Tracker) State() ActualState { return t.state }

// Left is the state a compaction under way left, which is empty in any other state.
func (t *Tracker) Left() ActualState {
	if t.state != Compacting {
		return ""
	}
	return t.resume
}

// Apply moves the lane, and reports whether it moved. A compaction returns to the state
// it left, whoever started it.
func (t *Tracker) Apply(signal Signal) bool {
	if signal == OnCompactionFinished {
		signal = OnFinishedAtRest
		if t.resume == Working {
			signal = OnFinishedInTurn
		}
	}
	next, moved := Next(t.state, signal)
	if !moved {
		return false
	}
	if next == Compacting {
		t.resume = t.state
	}
	t.state = next
	return true
}

// Condition is a flag on a state, not a state.
type Condition string

// The conditions a lane can carry.
const (
	Stalled          Condition = "stalled"
	Overdue          Condition = "overdue"
	WaitingExternal  Condition = "waiting_external"
	NearBudget       Condition = "near_budget"
	BriefUnconfirmed Condition = "brief_unconfirmed"
	PackOversized    Condition = "pack_oversized"
	HandoffDegraded  Condition = "handoff_degraded"
	DegradedMode     Condition = "degraded_mode"
	UnpushedWork     Condition = "unpushed_work"
	Drifted          Condition = "drifted"
)

// Facts is what a lane's conditions derive from.
type Facts struct {
	State ActualState
	// SinceProgress is how long the lane has shown no progress, and StallAfter how long
	// it may. Zero disables the check.
	SinceProgress time.Duration
	StallAfter    time.Duration
	PastDeadline  bool
	// WaitDeclared is a wait from `lane_wait`, and ToolInFlight a tool call that started
	// and has not finished. A quiet lane is not always a stuck one.
	WaitDeclared bool
	ToolInFlight bool
	// ContextTokens is the last call's context, and WarnTokens the profile's warning.
	ContextTokens int64
	WarnTokens    int64

	BriefUnconfirmed bool
	PackOversized    bool
	HandoffDegraded  bool
	Degraded         bool
	UnpushedWork     bool
	Drifted          bool
}

// Conditions derives a lane's conditions, in the design's order. `waiting_external`
// suppresses `stalled` and not `overdue`. A compaction reports nothing while it runs and nothing if
// it hangs, so the same limit bounds it, and it holds no tool call, so a call a hook left open
// excuses nothing.
func Conditions(f Facts) []Condition {
	var out []Condition
	waiting := f.WaitDeclared || f.ToolInFlight
	raise := func(condition Condition, on bool) {
		if on {
			out = append(out, condition)
		}
	}
	quiet := f.StallAfter > 0 && f.SinceProgress >= f.StallAfter
	raise(Stalled, quiet && ((f.State == Working && !waiting) || (f.State == Compacting && !f.WaitDeclared)))
	raise(Overdue, f.PastDeadline)
	raise(WaitingExternal, waiting)
	raise(NearBudget, f.WarnTokens > 0 && f.ContextTokens >= f.WarnTokens)
	raise(BriefUnconfirmed, f.BriefUnconfirmed)
	raise(PackOversized, f.PackOversized)
	raise(HandoffDegraded, f.HandoffDegraded)
	raise(DegradedMode, f.Degraded)
	raise(UnpushedWork, f.UnpushedWork)
	raise(Drifted, f.Drifted)
	return out
}

// Evidence is what a worker has read about a process. Each piece proves less than it
// appears to.
type Evidence struct {
	// ExitConfirmed is positive evidence of an end.
	ExitConfirmed bool
	// Answered is an application handshake that came back.
	Answered bool
	// ProcessExists says only that a process has not exited.
	ProcessExists bool
	// Incomplete says a read failed or timed out, which is an error and not an answer.
	Incomplete bool
}

// JudgeLiveness reads evidence. `dead` needs a confirmed exit, and anything short of a
// handshake is `unknown`, which restarts nothing.
func JudgeLiveness(e Evidence) Liveness {
	switch {
	case e.ExitConfirmed:
		return LivenessDead
	case e.Answered:
		return LivenessAlive
	}
	return LivenessUnknown
}

// Reachability is whether a session can reach something, apart from whether its machine
// is healthy (FR-LANE-7).
type Reachability string

// The values of Reachability.
const (
	Reachable    Reachability = "reachable"
	Impaired     Reachability = "impaired"
	Unreachable  Reachability = "unreachable"
	ReachUnknown Reachability = "unknown"
)

// ReachEvidence is the last thing seen about one target.
type ReachEvidence struct {
	Reach Reachability
	// Age is how old the evidence is.
	Age time.Duration
	// Seen says there is any.
	Seen bool
}

// ReadReach states a target's reachability. Only a lane doing something produces fresh
// evidence, so an idle session reads `unknown` and not `reachable`.
func ReadReach(state ActualState, evidence ReachEvidence, window time.Duration) Reachability {
	if !evidence.Seen || evidence.Age > window {
		return ReachUnknown
	}
	switch state {
	case Working, Compacting, NeedsDecision, HandingOff, Briefing, Parking:
		return evidence.Reach
	}
	return ReachUnknown
}

// ReachOfFailure reads a `StopFailure` category. A sign-in failure will not pass by
// itself, and every other failure, named or new, is a target that answered badly.
func ReachOfFailure(category string) Reachability {
	if category == "authentication_failed" {
		return Unreachable
	}
	return Impaired
}
