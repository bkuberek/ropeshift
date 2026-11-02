package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// Leases holds the use cases that decide who owns a work order. Each is one
// transaction, and the statements it composes do the deciding, so nothing can
// change hands between a check and a commit.
type Leases struct {
	store ports.Store
	// packTokens is the budget of the pack a claim pins. Zero takes the default.
	packTokens int64
}

// NewLeases returns the use cases over a Store.
func NewLeases(store ports.Store) *Leases {
	return &Leases{store: store}
}

// Budgeted sets the budget of the packs these claims pin.
func (l *Leases) Budgeted(tokens int64) *Leases {
	l.packTokens = tokens
	return l
}

// Claim grants a work order. A retry with the same claim ID returns the original
// grant, and a claim that ended stays lost.
func (l *Leases) Claim(ctx context.Context, scope ledger.WorkspaceID, request workorders.ClaimRequest, actor ledger.Actor) (workorders.Grant, error) {
	var grant workorders.Grant
	err := l.store.Write(ctx, scope, "claim", func(tx ports.WriteTx) error {
		existing, err := tx.Claims().Find(request.ClaimID)
		switch {
		case err == nil:
			grant, err = replayGrant(tx, existing, request)
			return err
		case !errors.Is(err, ports.ErrNotFound):
			return err
		}
		if err := checkEligible(tx, request); err != nil {
			return err
		}
		crashpoint.Hit("claim", "eligible")
		won, err := tx.Leases().Grant(request)
		if err != nil {
			return err
		}
		if !won {
			if _, err := tx.WorkOrders().Get(request.WorkOrder); err != nil {
				return err
			}
			return workorders.ErrLost
		}
		crashpoint.Hit("claim", "granted")
		current, err := tx.WorkOrders().Get(request.WorkOrder)
		if err != nil {
			return err
		}
		claim := workorders.Claim{
			ID:        request.ClaimID,
			WorkOrder: request.WorkOrder,
			Worker:    request.Worker,
			Fence:     current.Fence,
			GrantedAt: tx.Now(),
		}
		if err := continuesLane(tx, current, &claim); err != nil {
			return err
		}
		if err := l.pinPack(tx, current, &claim, actor); err != nil {
			return err
		}
		if err := tx.Claims().Record(claim); err != nil {
			return err
		}
		crashpoint.Hit("claim", "recorded")
		if grant, err = grantOf(tx, current, request); err != nil {
			return err
		}
		event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerClaimGranted), request.WorkOrder, workorders.Change{
			Trigger:    workorders.TriggerClaimGranted,
			Fence:      current.Fence,
			ClaimID:    request.ClaimID,
			Worker:     request.Worker,
			LeaseUntil: current.LeaseUntil,
			Lane:       claim.Lane,
		}, actor)
		if err != nil {
			return err
		}
		appended, err := tx.Append(event)
		if err != nil {
			return err
		}
		grant.Rev = appended.Rev
		return nil
	})
	if err != nil {
		return workorders.Grant{}, err
	}
	return grant, nil
}

// continuesLane makes a claim on woken work the claim of the lane it woke, with that lane's own
// profile layer, so the next claim resumes it and nobody dispatches anything.
func continuesLane(tx ports.WriteTx, order workorders.WorkOrder, claim *workorders.Claim) error {
	if order.ResumeLaneID == "" {
		return nil
	}
	claim.Lane = order.ResumeLaneID
	last, err := tx.Claims().OfLane(order.ResumeLaneID)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	claim.Overrides, claim.PackID = last.Overrides, last.PackID
	return err
}

func grantOf(tx ports.WriteTx, w workorders.WorkOrder, request workorders.ClaimRequest) (workorders.Grant, error) {
	grant := workorders.Grant{
		Lease:           workorders.Lease{WorkOrder: w.ID, ClaimID: request.ClaimID, Fence: w.Fence},
		TTLMs:           request.TTLMs,
		ResumeLaneID:    w.ResumeLaneID,
		Branch:          w.Branch,
		ContractID:      w.ContractID,
		ContractVersion: w.ContractVersion,
	}
	if w.ContractVersion == 0 {
		return grant, nil
	}
	contract, err := tx.Planning().Contract(w.ContractID, w.ContractVersion)
	if err != nil {
		return workorders.Grant{}, err
	}
	grant.ContractDigest = contract.Digest
	return grant, nil
}

// replayGrant answers a retry. It changes nothing, so the transaction rolls back
// and leaves no gap.
func replayGrant(tx ports.WriteTx, claim workorders.Claim, request workorders.ClaimRequest) (workorders.Grant, error) {
	current, err := tx.WorkOrders().Get(claim.WorkOrder)
	if err != nil {
		return workorders.Grant{}, err
	}
	if claim.Ended() || current.ClaimID != claim.ID {
		return workorders.Grant{}, workorders.ErrLost
	}
	grant, err := grantOf(tx, current, request)
	if err != nil {
		return workorders.Grant{}, err
	}
	grant.Fence = claim.Fence
	grant.Replayed = true
	return grant, nil
}

// checkEligible reads the workspace after the revision was taken, so no count can change
// before the grant, and a claim that races another at a cap loses.
func checkEligible(tx ports.WriteTx, request workorders.ClaimRequest) error {
	workspace, err := tx.Workspace()
	if err != nil {
		return err
	}
	if workspace.FleetMode != ledger.FleetRun {
		return fmt.Errorf("%w: the fleet is in %s", workorders.ErrIneligible, workspace.FleetMode)
	}
	if err := underCap(workspace.WIPLimit, "work in progress", tx.Leases().WorkInProgress); err != nil {
		return err
	}
	order, err := tx.WorkOrders().Get(request.WorkOrder)
	if err != nil {
		return err
	}
	if order.RepositoryID != "" {
		count := func() (int64, error) { return tx.Leases().InRepository(order.RepositoryID) }
		if err := underCap(workspace.RepositoryLimit, "lanes in this repository", count); err != nil {
			return err
		}
	}
	admitted, err := admitsResume(tx.Lanes(), order, request.Worker)
	if err != nil {
		return err
	}
	if !admitted {
		return fmt.Errorf("%w: worker %s does not hold lane %s", workorders.ErrIneligible, request.Worker, order.ResumeLaneID)
	}
	limit := workspace.WorkerLimit
	record, err := tx.Workers().Find(request.Worker)
	switch {
	case err == nil:
		if runs, why := record.Manifest.Runs(order.Needs); !runs {
			return fmt.Errorf("%w: worker %s has %s", workorders.ErrIneligible, request.Worker, why)
		}
		limit = tighter(limit, record.Manifest.Capacity.MaxLanes)
	case !errors.Is(err, ports.ErrNotFound):
		return err
	}
	count := func() (int64, error) { return tx.Leases().HeldByWorker(request.Worker) }
	return underCap(limit, "lanes on this worker", count)
}

// admitsResume says affinity lets this worker take a work order. Work with no lane to resume
// goes to anyone.
func admitsResume(lanes ports.LaneReader, order workorders.WorkOrder, worker string) (bool, error) {
	if order.ResumeLaneID == "" {
		return true, nil
	}
	row, err := lanes.Row(order.ResumeLaneID)
	if err != nil {
		return false, err
	}
	return row.Hold.Admits(worker), nil
}

// tighter picks the smaller of two limits, where zero means none.
func tighter(a, b int64) int64 {
	if a == 0 || (b != 0 && b < a) {
		return b
	}
	return a
}

// underCap reads a count only when a cap is set, because most workspaces set none.
func underCap(limit int64, what string, count func() (int64, error)) error {
	if limit == 0 {
		return nil
	}
	current, err := count()
	if err != nil {
		return err
	}
	if current >= limit {
		return fmt.Errorf("%w: %s at %d of %d", workorders.ErrIneligible, what, current, limit)
	}
	return nil
}

// Renew extends a lease as part of the heartbeat.
func (l *Leases) Renew(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, ttlMs int64) error {
	return l.store.Renew(ctx, scope, lease, ttlMs)
}

// Sweep lapses expired leases, each in its own short transaction. The candidates
// come from a read with no lock, so the lapse statement repeats the expiry
// predicate, and a lease renewed since the read stays.
func (l *Leases) Sweep(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error) {
	var candidates []string
	err := l.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		candidates, err = tx.WorkOrders().Expired(now)
		return err
	})
	if err != nil {
		return nil, err
	}
	crashpoint.Hit("sweep", "candidates-read")
	var lapsed []string
	for _, id := range candidates {
		changed, err := l.lapse(ctx, scope, id, actor)
		if err != nil {
			return lapsed, err
		}
		if changed {
			lapsed = append(lapsed, id)
		}
	}
	return lapsed, nil
}

func (l *Leases) lapse(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (bool, error) {
	changed := false
	err := l.store.Write(ctx, scope, "lapse", func(tx ports.WriteTx) error {
		won, err := tx.Leases().Lapse(id)
		if err != nil || !won {
			return err
		}
		crashpoint.Hit("lapse", "lapsed")
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerLeaseExpired), id, workorders.Change{
			Trigger: workorders.TriggerLeaseExpired,
			Fence:   current.Fence,
			ClaimID: current.ClaimID,
		}, actor)
		if err != nil {
			return err
		}
		if _, err := tx.Append(event); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// Return restores a lapsed lease to the worker that holds the current fence, in
// the state that lapsed. A retry that finds the lease already restored is done.
func (l *Leases) Return(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, ttlMs int64, actor ledger.Actor) error {
	return l.store.Write(ctx, scope, "return", func(tx ports.WriteTx) error {
		started, err := tx.Lanes().StartedUnder(lease.WorkOrder, lease.Fence)
		if err != nil {
			return err
		}
		restored, trigger := workorders.Claimed, workorders.TriggerReturnedUnstarted
		if started {
			restored, trigger = workorders.Running, workorders.TriggerReturnedStarted
		}
		won, err := tx.Leases().Return(lease, restored, ttlMs)
		if err != nil {
			return err
		}
		current, err := tx.WorkOrders().Get(lease.WorkOrder)
		if err != nil {
			return err
		}
		if !won {
			if current.State == restored && current.Fence == lease.Fence && current.ClaimID == lease.ClaimID {
				return nil
			}
			return &workorders.FencedError{Current: current.Fence, State: current.State}
		}
		crashpoint.Hit("return", "restored")
		event, err := workorders.ChangeEvent(workorders.EventType(trigger), lease.WorkOrder, workorders.Change{
			Trigger:    trigger,
			Fence:      lease.Fence,
			ClaimID:    lease.ClaimID,
			LeaseUntil: current.LeaseUntil,
		}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

// Release gives a claim back before its lane starts. The fence moves in the same
// statement, so the holder's token is stale at once. A retry that finds its own
// claim released is done.
func (l *Leases) Release(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, actor ledger.Actor) error {
	return l.store.Write(ctx, scope, "release", func(tx ports.WriteTx) error {
		won, err := tx.Leases().Release(lease)
		if err != nil {
			return err
		}
		if !won {
			return rejectRelease(tx, lease)
		}
		crashpoint.Hit("release", "released")
		if err := tx.Claims().End(lease.ClaimID, workorders.EndReleased); err != nil {
			return err
		}
		return appendOwnershipEnd(tx, lease.WorkOrder, workorders.TriggerClaimReleased, lease.ClaimID, "", actor)
	})
}

func rejectRelease(tx ports.WriteTx, lease workorders.Lease) error {
	claim, err := tx.Claims().Find(lease.ClaimID)
	if err != nil {
		return err
	}
	if claim.Ended() && claim.EndReason == workorders.EndReleased {
		return nil
	}
	current, err := tx.WorkOrders().Get(lease.WorkOrder)
	if err != nil {
		return err
	}
	return workorders.Reject(current, lease)
}

func appendOwnershipEnd(tx ports.WriteTx, id string, trigger workorders.Trigger, claimID, reason string, actor ledger.Actor) error {
	current, err := tx.WorkOrders().Get(id)
	if err != nil {
		return err
	}
	event, err := workorders.ChangeEvent(workorders.EventType(trigger), id, workorders.Change{
		Trigger: trigger,
		Fence:   current.Fence,
		ClaimID: claimID,
		Reason:  reason,
	}, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// Reassign ends a lapsed holder's ownership by a person's or an orchestrator's
// decision (D14), through the revocation statement. The work order returns to
// `ready`, and the actor and reason stay on record.
func (l *Leases) Reassign(ctx context.Context, scope ledger.WorkspaceID, workOrder, reason string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionReassign) {
		return workorders.ErrForbidden
	}
	return l.store.Write(ctx, scope, "reassign", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(workOrder)
		if err != nil {
			return err
		}
		if current.State != workorders.Lapsed {
			return workorders.ErrNotLapsed
		}
		won, err := tx.Leases().Revoke(workOrder, current.Fence)
		if err != nil {
			return err
		}
		if !won {
			return workorders.ErrNotLapsed
		}
		crashpoint.Hit("reassign", "revoked")
		if err := tx.Claims().End(current.ClaimID, workorders.EndReassigned); err != nil {
			return err
		}
		if err := retireWakes(tx, workOrder, parking.RetiredReassignment); err != nil {
			return err
		}
		if err := rollWorkOrder(tx, workOrder, actor); err != nil {
			return err
		}
		return appendOwnershipEnd(tx, workOrder, workorders.TriggerTakeover, current.ClaimID, reason, actor)
	})
}

// StartLane moves a claimed work order to running and records the lane's first
// generation under this fence. A retry after a lost answer returns the generation
// the lane already has.
func (l *Leases) StartLane(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, lane string, actor ledger.Actor) (int64, error) {
	var generation int64
	err := l.store.Write(ctx, scope, "lane.start", func(tx ports.WriteTx) error {
		var err error
		generation, err = startLane(tx, lease, lane, actor, func(*ledger.EventDraft) {})
		return err
	})
	if err != nil {
		return 0, err
	}
	return generation, nil
}

// startLane runs inside a write transaction, because a worker's upload starts a lane
// in the same transaction that stores the event. stamp gives the event its identity.
func startLane(tx ports.WriteTx, lease workorders.Lease, lane string, actor ledger.Actor, stamp func(*ledger.EventDraft)) (int64, error) {
	won, err := tx.Leases().Start(lease)
	if err != nil {
		return 0, err
	}
	if !won {
		current, err := tx.WorkOrders().Get(lease.WorkOrder)
		if err != nil {
			return 0, err
		}
		started, err := tx.Lanes().StartedUnder(lease.WorkOrder, lease.Fence)
		if err != nil {
			return 0, err
		}
		if current.State == workorders.Running && current.Fence == lease.Fence && started {
			return tx.Lanes().Generation(lane)
		}
		return 0, workorders.Reject(current, lease)
	}
	crashpoint.Hit("lane.start", "started")
	if err := tx.Lanes().Start(lane, lease.WorkOrder, lease.Fence); err != nil {
		return 0, err
	}
	first := ledger.Generation{Lane: lane, Number: 1, StartReason: ledger.StartInitial}
	if err := tx.Lanes().StartGeneration(first); err != nil {
		return 0, err
	}
	event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerLaneStarted), lease.WorkOrder, workorders.Change{
		Trigger:    workorders.TriggerLaneStarted,
		Fence:      lease.Fence,
		ClaimID:    lease.ClaimID,
		Lane:       lane,
		Generation: 1,
	}, actor)
	if err != nil {
		return 0, err
	}
	stamp(&event)
	_, err = tx.Append(event)
	return 1, err
}

// Dispatch records a member's or an orchestrator's request to run a lane on a claimed
// work order (FR-WO-9), which is that lane's desired state. It does not start anything:
// the worker holding the claim reads it and converges. A retry of the same lane is
// done, and a claim runs one lane.
func (l *Leases) Dispatch(ctx context.Context, scope ledger.WorkspaceID, workOrder, lane string, actor ledger.Actor) (workorders.Lease, error) {
	return l.DispatchWith(ctx, scope, workOrder, lane, "", actor)
}

// DispatchWith is a dispatch with a profile layer of the lane's own. It stays TOML here, and
// the worker that reads it decides what it may change.
func (l *Leases) DispatchWith(ctx context.Context, scope ledger.WorkspaceID, workOrder, lane, overrides string, actor ledger.Actor) (workorders.Lease, error) {
	if !authority.Allowed(actor, authority.ActionDispatchLane) {
		return workorders.Lease{}, workorders.ErrForbidden
	}
	if lane == "" {
		return workorders.Lease{}, fmt.Errorf("%w: a lane needs an ID", workorders.ErrInvalid)
	}
	var lease workorders.Lease
	err := l.store.Write(ctx, scope, "lane.dispatch", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(workOrder)
		if err != nil {
			return err
		}
		if current.State != workorders.Claimed {
			return fmt.Errorf("%w: dispatch from %s", workorders.ErrIllegalMove, current.State)
		}
		lease = workorders.Lease{WorkOrder: workOrder, ClaimID: current.ClaimID, Fence: current.Fence}
		claim, err := tx.Claims().Find(current.ClaimID)
		if err != nil {
			return err
		}
		if claim.Lane == lane {
			return nil
		}
		if claim.Lane != "" {
			return fmt.Errorf("%w: claim %s runs lane %s", workorders.ErrIllegalMove, claim.ID, claim.Lane)
		}
		if _, err := tx.Leases().Guard(workOrder, current.Fence); err != nil {
			return err
		}
		if err := tx.Claims().Dispatch(current.ClaimID, lane, overrides); err != nil {
			return err
		}
		crashpoint.Hit("lane.dispatch", "recorded")
		event, err := workorders.ChangeEvent(workorders.EventLaneDispatched, workOrder, workorders.Change{
			Guarded: true, Fence: current.Fence, ClaimID: current.ClaimID, Lane: lane,
		}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		return workorders.Lease{}, err
	}
	return lease, nil
}

// EndLane records a lane's outcome under its fence and generation.
func (l *Leases) EndLane(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, result workorders.LaneResult, actor ledger.Actor) error {
	return l.store.Write(ctx, scope, "lane.end", func(tx ports.WriteTx) error {
		return endLane(tx, holder, result, actor, func(*ledger.EventDraft) {})
	})
}

// endLane closes a lane in the transaction that stores its outcome: the state, the
// fence, the claim, and the event commit together. Only the current generation can
// end a lane, and a holder that lapsed returns first.
func endLane(tx ports.WriteTx, holder workorders.Holder, result workorders.LaneResult, actor ledger.Actor, stamp func(*ledger.EventDraft)) error {
	if err := result.Validate(); err != nil {
		return err
	}
	current, err := tx.WorkOrders().Get(holder.WorkOrder)
	if err != nil {
		return err
	}
	generation, err := tx.Lanes().Generation(holder.Lane)
	if err != nil {
		return err
	}
	if generation != holder.Generation {
		return fenced(tx, holder.WorkOrder)
	}
	ending, err := workorders.EndOf(current, result.Outcome)
	if err != nil {
		return err
	}
	won, err := tx.Leases().End(holder.Lease, ending.To, ending.Attempts)
	if err != nil {
		return err
	}
	if !won {
		return workorders.Reject(current, holder.Lease)
	}
	crashpoint.Hit("lane.end", "ended")
	if err := tx.Claims().End(holder.ClaimID, string(result.Outcome)); err != nil {
		return err
	}
	if err := tx.Lanes().MarkEnded(holder.Lane); err != nil {
		return err
	}
	if err := tx.Lanes().EndGeneration(holder.Lane, holder.Generation, string(result.Outcome)); err != nil {
		return err
	}
	if err := rollMessages(tx, holder.Lane, holder.Generation, false, actor); err != nil {
		return err
	}
	if err := retireWakes(tx, holder.WorkOrder, parking.RetiredFinished); err != nil {
		return err
	}
	if err := recordEvidence(tx, holder, current.Fence+1, result); err != nil {
		return err
	}
	crashpoint.Hit("lane.end", "evidence")
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	event, err := workorders.ChangeEvent(workorders.EventType(ending.Trigger), holder.WorkOrder, workorders.Change{
		Trigger: ending.Trigger, Fence: current.Fence + 1, ClaimID: holder.ClaimID, Lane: holder.Lane,
		Generation: holder.Generation, Outcome: result.Outcome, Reason: result.Reason, Attempts: ending.Attempts,
		Payload: payload,
	}, actor)
	if err != nil {
		return err
	}
	stamp(&event)
	_, err = tx.Append(event)
	return err
}

// Fenced runs a write under the holder's fence and generation. The comparison is a
// guarded statement inside the transaction that writes, so ownership cannot change
// between the check and the commit.
func (l *Leases) Fenced(ctx context.Context, scope ledger.WorkspaceID, op string, holder workorders.Holder, fn func(ports.WriteTx) error) error {
	return l.store.Write(ctx, scope, op, func(tx ports.WriteTx) error {
		if err := guardHolder(tx, op, holder); err != nil {
			return err
		}
		return fn(tx)
	})
}

// guardHolder checks a holder's fence and generation inside a write transaction.
func guardHolder(tx ports.WriteTx, op string, holder workorders.Holder) error {
	won, err := tx.Leases().Guard(holder.WorkOrder, holder.Fence)
	if err != nil {
		return err
	}
	if !won {
		return fenced(tx, holder.WorkOrder)
	}
	crashpoint.Hit(op, "guarded")
	if holder.Lane == "" {
		return nil
	}
	generation, err := tx.Lanes().Generation(holder.Lane)
	if err != nil {
		return err
	}
	if generation != holder.Generation {
		return fenced(tx, holder.WorkOrder)
	}
	return nil
}

func fenced(tx ports.WriteTx, workOrder string) error {
	current, err := tx.WorkOrders().Get(workOrder)
	if err != nil {
		return err
	}
	return &workorders.FencedError{Current: current.Fence, State: current.State}
}

// Write appends one event under the holder's fence. The event carries the fence,
// so a replay can check it and count the write. The client generates writeID, and
// a retry finds the first attempt before it checks anything, so a write whose
// answer was lost cannot repeat, even if the fence moved since.
func (l *Leases) Write(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, writeID string, payload []byte, actor ledger.Actor) (ledger.Event, error) {
	var written ledger.Event
	err := l.store.Write(ctx, scope, "fenced_write", func(tx ports.WriteTx) error {
		first, err := tx.Event(writeID)
		if err == nil {
			written = first
			return nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		if err := guardHolder(tx, "fenced_write", holder); err != nil {
			return err
		}
		event, err := workorders.ChangeEvent(workorders.EventFencedWrite, holder.WorkOrder, workorders.Change{
			Guarded:    true,
			Fence:      holder.Fence,
			ClaimID:    holder.ClaimID,
			Lane:       holder.Lane,
			Generation: holder.Generation,
			Payload:    payload,
		}, actor)
		if err != nil {
			return err
		}
		event.ID = writeID
		written, err = tx.Append(event)
		return err
	})
	return written, err
}

// The operations and the events of a lane's move to its next generation.
const (
	opLaneRestart = "lane.restart"
	opLaneHandoff = "lane.handoff"
	opLaneResume  = "lane.resume"
)

// nextGeneration moves a lane on under the claim it holds, so what the last generation still
// has in flight is refused. The move records why, and a handoff names the document the new
// generation reads.
func nextGeneration(tx ports.WriteTx, holder workorders.Holder, reason ledger.StartReason, actor ledger.Actor, stamp func(*ledger.EventDraft)) (int64, error) {
	op, eventType := opLaneRestart, workorders.EventLaneRestarted
	switch reason {
	case ledger.StartHandoff:
		op, eventType = opLaneHandoff, workorders.EventLaneHandoff
	case ledger.StartResume:
		op, eventType = opLaneResume, workorders.EventLaneResumed
	}
	if err := guardHolder(tx, op, holder); err != nil {
		return 0, err
	}
	next, err := tx.Lanes().NextGeneration(holder.Lane)
	if err != nil {
		return 0, err
	}
	if err := tx.Lanes().EndGeneration(holder.Lane, holder.Generation, string(reason)); err != nil {
		return 0, err
	}
	if err := rollMessages(tx, holder.Lane, holder.Generation, true, actor); err != nil {
		return 0, err
	}
	started := ledger.Generation{Lane: holder.Lane, Number: next, StartReason: reason}
	left, err := tx.Lanes().Handoff(holder.Lane, holder.Generation)
	switch {
	case err == nil && reason == ledger.StartHandoff:
		started.HandoffIn = left.ArtifactID
	case err != nil && !errors.Is(err, ports.ErrNotFound):
		return 0, err
	}
	if err := tx.Lanes().StartGeneration(started); err != nil {
		return 0, err
	}
	event, err := workorders.ChangeEvent(eventType, holder.WorkOrder, workorders.Change{
		Guarded: true, Fence: holder.Fence, ClaimID: holder.ClaimID, Lane: holder.Lane, Generation: next,
	}, actor)
	if err != nil {
		return 0, err
	}
	stamp(&event)
	_, err = tx.Append(event)
	return next, err
}

// ContinueLane starts a woken lane's next generation under the claim that took it. The holder
// names that claim and the generation that parked. A retry after a lost answer returns the
// generation the lane already moved to.
func (l *Leases) ContinueLane(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, reason ledger.StartReason, actor ledger.Actor) (int64, error) {
	var next int64
	err := l.store.Write(ctx, scope, opLaneResume, func(tx ports.WriteTx) error {
		var err error
		next, err = continueLane(tx, holder, reason, actor, noStamp)
		return err
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

// continueLane runs inside a write transaction, because a worker's upload resumes a lane in the
// same transaction that stores the event. The work order runs again, the lane moves to this
// claim's fence, and the lane to resume clears, all together.
func continueLane(tx ports.WriteTx, holder workorders.Holder, reason ledger.StartReason, actor ledger.Actor, stamp func(*ledger.EventDraft)) (int64, error) {
	won, err := tx.Leases().Start(holder.Lease)
	if err != nil {
		return 0, err
	}
	if !won {
		return resumedAlready(tx, holder)
	}
	crashpoint.Hit("lane.resume", "started")
	if err := tx.Lanes().Resume(holder.Lane, holder.Fence); err != nil {
		return 0, err
	}
	event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerLaneStarted), holder.WorkOrder, workorders.Change{
		Trigger: workorders.TriggerLaneStarted, Fence: holder.Fence, ClaimID: holder.ClaimID, Lane: holder.Lane, Generation: holder.Generation + 1,
	}, actor)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Append(event); err != nil {
		return 0, err
	}
	crashpoint.Hit("lane.resume", "recorded")
	return nextGeneration(tx, holder, reason, actor, stamp)
}

// resumedAlready answers a resume that matched nothing, which a retry after a lost answer does.
func resumedAlready(tx ports.WriteTx, holder workorders.Holder) (int64, error) {
	current, err := tx.WorkOrders().Get(holder.WorkOrder)
	if err != nil {
		return 0, err
	}
	generation, err := tx.Lanes().Generation(holder.Lane)
	if err != nil {
		return 0, err
	}
	repeat := current.State == workorders.Running && current.Fence == holder.Fence && current.ClaimID == holder.ClaimID && generation == holder.Generation+1
	if repeat && !refusesResumeRetry() {
		return generation, nil
	}
	return 0, workorders.Reject(current, holder.Lease)
}

// resumeUpload starts a woken lane's next generation on a worker's word. The event names the new
// generation, as a restart's does. The claim must be this worker's, and for this lane.
func resumeUpload(tx ports.WriteTx, worker string, e ledger.UploadEvent, stamp func(*ledger.EventDraft)) error {
	claim, err := tx.Claims().Find(e.ClaimID)
	if err != nil {
		return err
	}
	if (claim.Worker != worker || claim.Lane != e.Lane) && !resumesAnyClaim() {
		return fmt.Errorf("%w: claim %s does not run lane %s here", workorders.ErrInvalid, e.ClaimID, e.Lane)
	}
	holder := workorders.Holder{
		Lease: workorders.Lease{WorkOrder: e.WorkOrder, ClaimID: e.ClaimID, Fence: e.Fence},
		Lane:  e.Lane, Generation: e.Generation - 1,
	}
	_, err = continueLane(tx, holder, startReason(e), ledger.Actor{Kind: ledger.ActorWorker, ID: worker}, stamp)
	return err
}

// ResetLane lets a work order's tripped lane start again. Only a person can, because the
// breaker exists to stop what no automatic step could fix. It goes through the work order,
// as a lane's start does: a person asks, and the lane's state still derives from what
// happens next.
func (l *Leases) ResetLane(ctx context.Context, scope ledger.WorkspaceID, workOrder string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionResetLane) {
		return workorders.ErrForbidden
	}
	return l.store.Write(ctx, scope, "lane.reset", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(workOrder)
		if err != nil {
			return err
		}
		claim, err := tx.Claims().Find(current.ClaimID)
		if err != nil {
			return err
		}
		if claim.Lane == "" {
			return fmt.Errorf("%w: work order %s runs no lane", workorders.ErrIllegalMove, workOrder)
		}
		if _, err := tx.Lanes().Reset(claim.Lane); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: ledger.EventLaneReset, Actor: actor, Subject: ledger.Subject{Kind: ledger.SubjectLane, ID: claim.Lane},
		})
		return err
	})
}

// RespawnLane asks for the next generation of a work order's running lane (FR-LANE-10). A person
// or an orchestrator asks, and the lane's state still derives from what happens next.
func (l *Leases) RespawnLane(ctx context.Context, scope ledger.WorkspaceID, workOrder string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionRespawnLane) {
		return workorders.ErrForbidden
	}
	return l.store.Write(ctx, scope, "lane.respawn", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(workOrder)
		if err != nil {
			return err
		}
		if current.State != workorders.Running {
			return fmt.Errorf("%w: work order %s is %s, so it has no session to replace", workorders.ErrIllegalMove, workOrder, current.State)
		}
		claim, err := tx.Claims().Find(current.ClaimID)
		if err != nil {
			return err
		}
		if err := tx.Lanes().Respawn(claim.Lane); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: ledger.EventLaneRespawn, Actor: actor, Subject: ledger.Subject{Kind: ledger.SubjectLane, ID: claim.Lane},
		})
		return err
	})
}

// ArchiveLane asks the worker that ran a lane to take its worktree away. Only a member can
// force that past work no remote holds, and nobody can remove a worktree a lane still uses.
func (l *Leases) ArchiveLane(ctx context.Context, scope ledger.WorkspaceID, workOrder, lane string, forced bool, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionArchiveLane) || (forced && !authority.Allowed(actor, authority.ActionForceArchive)) {
		return workorders.ErrForbidden
	}
	return l.store.Write(ctx, scope, "lane.archive", func(tx ports.WriteTx) error {
		row, err := tx.Lanes().Row(lane)
		if err != nil {
			return err
		}
		if row.WorkOrder != workOrder {
			return fmt.Errorf("lane %s of work order %s: %w", lane, workOrder, ports.ErrNotFound)
		}
		order, err := tx.WorkOrders().Get(workOrder)
		if err != nil {
			return err
		}
		if row.Status.ActualState != ledger.StateEnded && order.Fence == row.StartedFence {
			return fmt.Errorf("%w: lane %s is still running", workorders.ErrIllegalMove, lane)
		}
		if row.UnpushedWork && !forced {
			return fmt.Errorf("%w: lane %s", workorders.ErrUnpushedWork, lane)
		}
		if err := tx.Lanes().RequestArchive(lane, forced); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: ledger.EventLaneArchiveRequested, Actor: actor, Subject: ledger.Subject{Kind: ledger.SubjectLane, ID: lane},
		})
		return err
	})
}

// Handoff moves a lane to its next generation within the same claim, so the
// previous generation cannot write after it.
func (l *Leases) Handoff(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, actor ledger.Actor) (int64, error) {
	var next int64
	err := l.store.Write(ctx, scope, opLaneHandoff, func(tx ports.WriteTx) error {
		var err error
		next, err = nextGeneration(tx, holder, ledger.StartHandoff, actor, func(*ledger.EventDraft) {})
		return err
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}
