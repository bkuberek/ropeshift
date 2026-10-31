package control

import (
	"context"
	"errors"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// Leases holds the use cases that decide who owns a work order. Each is one
// transaction, and the statements it composes do the deciding, so nothing can
// change hands between a check and a commit.
type Leases struct {
	store ports.Store
}

// NewLeases returns the use cases over a Store.
func NewLeases(store ports.Store) *Leases {
	return &Leases{store: store}
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
		if err := checkEligible(tx); err != nil {
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
		err = tx.Claims().Record(workorders.Claim{
			ID:        request.ClaimID,
			WorkOrder: request.WorkOrder,
			Worker:    request.Worker,
			Fence:     current.Fence,
			GrantedAt: tx.Now(),
		})
		if err != nil {
			return err
		}
		crashpoint.Hit("claim", "recorded")
		grant = grantOf(current, request)
		event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerClaimGranted), request.WorkOrder, workorders.Change{
			Trigger:    workorders.TriggerClaimGranted,
			Fence:      current.Fence,
			ClaimID:    request.ClaimID,
			Worker:     request.Worker,
			LeaseUntil: current.LeaseUntil,
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

func grantOf(w workorders.WorkOrder, request workorders.ClaimRequest) workorders.Grant {
	return workorders.Grant{
		Lease:           workorders.Lease{WorkOrder: w.ID, ClaimID: request.ClaimID, Fence: w.Fence},
		TTLMs:           request.TTLMs,
		ResumeLaneID:    w.ResumeLaneID,
		Branch:          w.Branch,
		ContractID:      w.ContractID,
		ContractVersion: w.ContractVersion,
	}
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
	grant := grantOf(current, request)
	grant.Fence = claim.Fence
	grant.Replayed = true
	return grant, nil
}

// checkEligible reads the workspace after the revision was taken, so the count
// cannot change before the grant.
func checkEligible(tx ports.WriteTx) error {
	workspace, err := tx.Workspace()
	if err != nil {
		return err
	}
	if workspace.FleetMode != ledger.FleetRun {
		return workorders.ErrIneligible
	}
	if workspace.WIPLimit == 0 {
		return nil
	}
	inProgress, err := tx.Leases().WorkInProgress()
	if err != nil {
		return err
	}
	if inProgress >= workspace.WIPLimit {
		return workorders.ErrIneligible
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
	if actor.Kind != ledger.ActorMember && actor.Kind != ledger.ActorOrchestrator {
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
		return appendOwnershipEnd(tx, workOrder, workorders.TriggerTakeover, current.ClaimID, reason, actor)
	})
}

// StartLane moves a claimed work order to running and records the lane's first
// generation under this fence. A retry after a lost answer returns the generation
// the lane already has.
func (l *Leases) StartLane(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, lane string, actor ledger.Actor) (int64, error) {
	var generation int64
	err := l.store.Write(ctx, scope, "lane.start", func(tx ports.WriteTx) error {
		won, err := tx.Leases().Start(lease)
		if err != nil {
			return err
		}
		if !won {
			current, err := tx.WorkOrders().Get(lease.WorkOrder)
			if err != nil {
				return err
			}
			started, err := tx.Lanes().StartedUnder(lease.WorkOrder, lease.Fence)
			if err != nil {
				return err
			}
			if current.State == workorders.Running && current.Fence == lease.Fence && started {
				generation, err = tx.Lanes().Generation(lane)
				return err
			}
			return workorders.Reject(current, lease)
		}
		crashpoint.Hit("lane.start", "started")
		if err := tx.Lanes().Start(lane, lease.WorkOrder, lease.Fence); err != nil {
			return err
		}
		generation = 1
		event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerLaneStarted), lease.WorkOrder, workorders.Change{
			Trigger:    workorders.TriggerLaneStarted,
			Fence:      lease.Fence,
			ClaimID:    lease.ClaimID,
			Lane:       lane,
			Generation: generation,
		}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		return 0, err
	}
	return generation, nil
}

// Fenced runs a write under the holder's fence and generation. The comparison is a
// guarded statement inside the transaction that writes, so ownership cannot change
// between the check and the commit.
func (l *Leases) Fenced(ctx context.Context, scope ledger.WorkspaceID, op string, holder workorders.Holder, fn func(ports.WriteTx) error) error {
	return l.store.Write(ctx, scope, op, func(tx ports.WriteTx) error {
		won, err := tx.Leases().Guard(holder.WorkOrder, holder.Fence)
		if err != nil {
			return err
		}
		if !won {
			return fenced(tx, holder.WorkOrder)
		}
		crashpoint.Hit(op, "guarded")
		if holder.Lane != "" {
			generation, err := tx.Lanes().Generation(holder.Lane)
			if err != nil {
				return err
			}
			if generation != holder.Generation {
				return fenced(tx, holder.WorkOrder)
			}
		}
		return fn(tx)
	})
}

func fenced(tx ports.WriteTx, workOrder string) error {
	current, err := tx.WorkOrders().Get(workOrder)
	if err != nil {
		return err
	}
	return &workorders.FencedError{Current: current.Fence, State: current.State}
}

// Write appends one event under the holder's fence. The event carries the fence,
// so a replay can check it and count the write.
func (l *Leases) Write(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, payload []byte, actor ledger.Actor) (ledger.Event, error) {
	var written ledger.Event
	err := l.Fenced(ctx, scope, "fenced_write", holder, func(tx ports.WriteTx) error {
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
		written, err = tx.Append(event)
		return err
	})
	return written, err
}

// Handoff moves a lane to its next generation within the same claim, so the
// previous generation cannot write after it.
func (l *Leases) Handoff(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, actor ledger.Actor) (int64, error) {
	var next int64
	err := l.Fenced(ctx, scope, "lane.handoff", holder, func(tx ports.WriteTx) error {
		var err error
		if next, err = tx.Lanes().NextGeneration(holder.Lane); err != nil {
			return err
		}
		event, err := workorders.ChangeEvent("work_order.lane_handoff", holder.WorkOrder, workorders.Change{
			Guarded:    true,
			Fence:      holder.Fence,
			ClaimID:    holder.ClaimID,
			Lane:       holder.Lane,
			Generation: next,
		}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}
