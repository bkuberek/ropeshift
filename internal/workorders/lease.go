package workorders

import (
	"errors"
	"fmt"
)

// Claim errors. Each is a different instruction to the caller.
var (
	// ErrLost means another worker holds the work order, or it is not ready.
	ErrLost = errors.New("claim lost")
	// ErrIneligible means the fleet mode or the work-in-progress limit refused
	// the claim.
	ErrIneligible = errors.New("claim refused by workspace policy")
	// ErrLapsed means the fence is current and the lease has expired, so the
	// holder should return.
	ErrLapsed = errors.New("lease lapsed")
	// ErrExpired means the fence is current and the deadline passed, and no sweep
	// has recorded the lapse. The holder keeps its claim and asks again, because a
	// lease past its deadline comes back only through a lapse on record.
	ErrExpired = errors.New("lease expired")
	// ErrUnpushedWork means a removal would lose work no remote holds.
	ErrUnpushedWork = errors.New("the worktree holds work no remote has")
	// ErrNotLapsed means a reassignment found a work order that is not lapsed.
	ErrNotLapsed = errors.New("work order is not lapsed")
	// ErrForbidden means this actor may not take this step.
	ErrForbidden = errors.New("actor may not do this")
	// ErrLeasedEdge means a transition changes a holder, so only a lease use case
	// may take it.
	ErrLeasedEdge = errors.New("transition needs the lease path")
	// ErrIllegalMove means the state machine has no such edge.
	ErrIllegalMove = errors.New("no such transition")
)

// FencedError is the 409 of FR-WO-3: the token is stale, and here is the current
// one. A fenced holder must stop and salvage its work.
type FencedError struct {
	Current int64
	State   State
}

func (e *FencedError) Error() string {
	return fmt.Sprintf("fenced: current fence %d, state %s", e.Current, e.State)
}

// Lease is what a holder presents on every later call.
type Lease struct {
	WorkOrder string
	ClaimID   string
	Fence     int64
}

// Holder is a lease plus the lane and generation writing under it. A worker's own
// write names no lane, and a lane's write must name the current generation.
type Holder struct {
	Lease
	Lane       string
	Generation int64
}

// ClaimRequest asks for a work order. The client generates ClaimID, so a retry
// after a timeout returns the original grant.
type ClaimRequest struct {
	WorkOrder string
	ClaimID   string
	Worker    string
	TTLMs     int64
}

// Grant is the claim response.
type Grant struct {
	Lease
	TTLMs           int64
	ResumeLaneID    string
	Branch          string
	ContractID      string
	ContractVersion int64
	ContractDigest  string
	// Rev is the revision of the grant event. A replayed grant has none.
	Rev      int64
	Replayed bool
}

// Claim is one grant of a work order to a worker. Its primary key stops a retry
// from being granted a second time after the claim ended.
type Claim struct {
	ID        string
	WorkOrder string
	Worker    string
	Fence     int64
	GrantedAt int64
	EndedAt   int64
	EndReason string
	// Lane is the lane a member or an orchestrator asked to run under this claim.
	Lane string
	// Overrides is that lane's own profile layer, as TOML.
	Overrides string
	// PackID names the context pack this claim pinned. A lane keeps it across generations and
	// across the claims that bring it back.
	PackID string
}

// Ended reports a claim that no longer holds anything.
func (c Claim) Ended() bool { return c.EndedAt != 0 }

// The reasons a claim ends.
const (
	EndReleased   = "released"
	EndReassigned = "reassigned"
	EndParked     = "parked"
	EndCanceled   = "canceled"
)

// Reject turns a guarded statement's row count of 0 into an instruction, because
// the count alone cannot tell a stale holder from a lapsed one.
func Reject(current WorkOrder, lease Lease) error {
	if current.State == Lapsed && current.Fence == lease.Fence && current.ClaimID == lease.ClaimID {
		return ErrLapsed
	}
	return &FencedError{Current: current.Fence, State: current.State}
}

// RejectRenewal is Reject for a renewal, whose statement also refuses a deadline that
// has passed. A row this holder still has can only have failed that predicate, so it
// is expired and not fenced, and the holder must not stop or salvage anything.
func RejectRenewal(current WorkOrder, lease Lease) error {
	holds := current.Fence == lease.Fence && current.ClaimID == lease.ClaimID
	if holds && (current.State == Claimed || current.State == Running) {
		return ErrExpired
	}
	return Reject(current, lease)
}
