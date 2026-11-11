package workorders

import "fmt"

// Start is where a lane's branch begins: a ref a person reads it by, and the commit that ref named when
// the claim was made. The worktree starts at the commit, so a branch that moves later changes nothing
// under a lane (FR-BASE-4).
type Start struct {
	Ref    string `json:"ref,omitempty"`
	Commit string `json:"commit,omitempty"`
	// ContinuesRef and ContinuesCommit are the in-flight branch a lane continues and its tip, where the
	// lane's branch begins (ADR-0023). Commit is then where that branch left the base, which review diffs
	// from, rather than where the lane's branch begins.
	ContinuesRef    string `json:"continues_ref,omitempty"`
	ContinuesCommit string `json:"continues_commit,omitempty"`
}

// HoldReason says why work that is ready cannot start yet.
type HoldReason string

// The reasons a start is held. Each is shown to a person, and each is retried on the next pass.
const (
	// HoldWaitingForMerge says a dependency's accepted work is not yet in the base branch.
	HoldWaitingForMerge HoldReason = "waiting_for_merge"
	// HoldDependenciesDiverge says the accepted work of the dependencies is on separate lines, and
	// none of it holds the others.
	HoldDependenciesDiverge HoldReason = "dependencies_diverge"
	// HoldBaseUnavailable says the base branch could not be read: the fetch failed, or no such branch
	// exists. A lane never starts from anything else.
	HoldBaseUnavailable HoldReason = "base_unavailable"
	// HoldContinuedBranchMissing says the in-flight branch the work continues is on neither the repository's
	// remote nor its checkout (ADR-0023).
	HoldContinuedBranchMissing HoldReason = "continued_branch_missing"
)

// StartHold is a reason a ready work order is not claimed, and what it waits for.
type StartHold struct {
	Reason HoldReason `json:"reason"`
	// Dependency names the work order whose work is awaited, when one is.
	Dependency string `json:"dependency,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// Error reads the hold as a refusal of a claim.
func (h StartHold) Error() string {
	text := string(h.Reason)
	if h.Dependency != "" {
		text += " " + h.Dependency
	}
	if h.Detail != "" {
		text += ": " + h.Detail
	}
	return fmt.Sprintf("%v: %s", ErrIneligible, text)
}

// Is makes a hold a refusal by policy, because the workspace's rules decided it.
func (h StartHold) Is(target error) bool { return target == ErrIneligible }

// Events of a start that is held and released. Neither changes the work order's state.
const (
	// EventStartHeld records the reason a ready work order is not claimed.
	EventStartHeld = "work_order.start_held"
	// EventStartResolved records that the reason no longer holds.
	EventStartResolved = "work_order.start_resolved"
)
