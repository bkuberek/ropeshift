package workorders

import (
	"cmp"
	"maps"
	"slices"
)

// Trigger is what moves a work order from one state to another.
type Trigger string

// The triggers of the state diagram.
const (
	TriggerAccepted          Trigger = "accepted"
	TriggerContractSubmitted Trigger = "contract_submitted"
	TriggerContractChange    Trigger = "contract_must_change"
	TriggerReadyPassed       Trigger = "ready_passed"
	TriggerBackToReview      Trigger = "back_to_review"
	TriggerClaimGranted      Trigger = "claim_granted"
	TriggerClaimReleased     Trigger = "claim_released"
	TriggerLaneStarted       Trigger = "lane_started"
	TriggerLeaseExpired      Trigger = "lease_expired"
	TriggerLaneParked        Trigger = "lane_parked"
	TriggerWakeFired         Trigger = "wake_fired"
	TriggerReturnedStarted   Trigger = "worker_returned_started"
	TriggerReturnedUnstarted Trigger = "worker_returned_unstarted"
	TriggerTakeover          Trigger = "takeover"
	TriggerLaneCompleted     Trigger = "lane_completed"
	TriggerAcceptancePassed  Trigger = "acceptance_passed"
	TriggerRework            Trigger = "rework"
	TriggerLaneBlocked       Trigger = "lane_blocked"
	TriggerReplan            Trigger = "replan"
	TriggerResolved          Trigger = "resolved"
	TriggerLaneFailed        Trigger = "lane_failed"
	TriggerAttemptsExhausted Trigger = "attempts_exhausted"
	TriggerRetried           Trigger = "retried"
	TriggerCanceled          Trigger = "canceled"
)

// TriggerLaneFailedRetry is a failed lane returning its work to `ready` on its own, which a build before
// retries were judged recorded (ADR-0014). Nothing takes it now, and a replay still reads it.
const TriggerLaneFailedRetry Trigger = "lane_failed_retry"

// Transition is one edge of the state machine. A leased edge changes who holds the
// work order, so only a lease use case may take it: the fence must move or be
// checked in the same transaction.
type Transition struct {
	From    State
	Trigger Trigger
	To      State
	Leased  bool
}

// edges is the state machine as data.
var edges = []Transition{
	{Intake, TriggerAccepted, Planning, false},
	{Planning, TriggerContractSubmitted, ContractReview, false},
	{ContractReview, TriggerContractChange, Planning, false},
	{ContractReview, TriggerReadyPassed, Ready, false},
	{Ready, TriggerBackToReview, ContractReview, false},
	{Ready, TriggerClaimGranted, Claimed, true},
	{Claimed, TriggerClaimReleased, Ready, true},
	{Claimed, TriggerLaneStarted, Running, true},
	{Claimed, TriggerLeaseExpired, Lapsed, true},
	{Running, TriggerLaneParked, Parked, true},
	{Parked, TriggerWakeFired, Ready, false},
	{Running, TriggerLeaseExpired, Lapsed, true},
	{Lapsed, TriggerReturnedStarted, Running, true},
	{Lapsed, TriggerReturnedUnstarted, Claimed, true},
	{Lapsed, TriggerTakeover, Ready, true},
	{Running, TriggerLaneCompleted, Acceptance, true},
	{Acceptance, TriggerAcceptancePassed, Done, false},
	{Acceptance, TriggerRework, Ready, false},
	{Running, TriggerLaneBlocked, Blocked, true},
	{Blocked, TriggerReplan, Planning, false},
	{Blocked, TriggerResolved, Ready, false},
	{Running, TriggerLaneFailed, Failed, true},
	{Running, TriggerAttemptsExhausted, Failed, true},
	{Failed, TriggerRetried, Ready, false},
}

// earlierEdges are edges a ledger an earlier build wrote can hold, which nothing takes now. A replay reads
// them, so a projection rebuilt from such a ledger is the one that build kept.
var earlierEdges = map[edgeKey]Transition{
	{Running, TriggerLaneFailedRetry}: {Running, TriggerLaneFailedRetry, Ready, true},
}

// States lists every state.
func States() []State {
	return []State{
		Intake, Planning, ContractReview, Ready, Claimed, Running, Parked,
		Lapsed, Acceptance, Blocked, Done, Failed, Canceled,
	}
}

// Terminal reports a state where work ends: no lane, tick, or cancel moves it. The one way out of an end
// is a retry of `failed` work, which an orchestrator judges within its recovery policy or a member makes,
// so a failed lane never spends its attempts unseen (ADR-0014).
func Terminal(s State) bool {
	return s == Done || s == Failed || s == Canceled
}

// holdsLease reports the states with a holder. Canceling one must end the claim,
// so that edge is leased too.
func holdsLease(s State) bool {
	return s == Claimed || s == Running || s == Lapsed
}

type edgeKey struct {
	from    State
	trigger Trigger
}

// table adds cancellation from every non-terminal state to the diagram's edges.
var table = func() map[edgeKey]Transition {
	out := make(map[edgeKey]Transition, len(edges)+len(States()))
	for _, e := range edges {
		out[edgeKey{e.From, e.Trigger}] = e
	}
	for _, s := range States() {
		if !Terminal(s) {
			out[edgeKey{s, TriggerCanceled}] = Transition{s, TriggerCanceled, Canceled, holdsLease(s)}
		}
	}
	return out
}()

// Transitions returns the whole table, in a stable order.
func Transitions() []Transition {
	all := slices.Collect(maps.Values(table))
	slices.SortFunc(all, func(a, b Transition) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.Trigger, b.Trigger))
	})
	return all
}

// Next returns the edge a trigger takes from a state.
func Next(from State, trigger Trigger) (Transition, bool) {
	edge, ok := table[edgeKey{from, trigger}]
	return edge, ok
}

// Taken returns the edge a recorded trigger took from a state: the table's, or one a ledger an earlier
// build wrote can hold. A reader of history uses it, and nothing that moves work does.
func Taken(from State, trigger Trigger) (Transition, bool) {
	if edge, ok := Next(from, trigger); ok {
		return edge, true
	}
	edge, ok := earlierEdges[edgeKey{from, trigger}]
	return edge, ok
}

// LeavesAcceptance reports the edges that end a lane's work, which answer to its evidence.
func LeavesAcceptance(trigger Trigger) bool {
	return trigger == TriggerAcceptancePassed || trigger == TriggerRework
}

// TakesNewVersion reports a state whose work has not started, so a new contract version can
// replace its pin. A lane that started keeps what it pinned, and a later version reaches it
// as a notice (FR-PLAN-8).
func TakesNewVersion(s State) bool {
	return s == Intake || s == Planning || s == ContractReview || s == Ready
}
