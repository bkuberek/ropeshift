package contract

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// TTLMs is the lease a check takes. Time moves only when a check moves it.
const TTLMs = int64(60_000)

// LeaseChecks are the invariants of the leases spike, numbered as it numbers them.
// L7 waits for automatic takeover in phase 3. The checks after L16 cover what
// phase 1 adds to them.
var LeaseChecks = []Check{
	{"L1", "at most one owner under racing claims", checkClaimRace},
	{"L2", "the fence strictly increases across restarts", checkFenceIncreases},
	{"L3", "a stale write is rejected with the current fence", checkStaleWrite},
	{"L4", "a write in flight during a handover is ordered before it or rejected", checkInFlightWrite},
	{"L5", "the old holder cannot write after ownership ends", checkWriteAfterRevocation},
	{"L6", "an expired lease lapses and does not transfer", checkLapseNotTransfer},
	{"L8", "renewal cannot revive a lease that was reassigned", checkRenewCannotRevive},
	{"L9", "a sweep's earlier read cannot lapse a lease that returned since", checkStaleSweep},
	{"L10", "lease time comes only from the database", checkLeaseTimeFromDatabase},
	{"L11", "a claim retry returns the original grant", checkClaimRetry},
	{"L12", "a crash in claimed reaches lapsed", checkClaimedLapses},
	{"L13", "a worker with the current fence can return", checkReturn},
	{"L14", "a return racing a reassignment has one winner", checkReturnRacesReassign},
	{"L15", "the work-in-progress limit and fleet mode hold under racing claims", checkEligibility},
	{"L16", "the previous generation cannot write after a handoff", checkGeneration},
	{"L17", "renewal takes no revision and appends no event", checkRenewalAddsNothing},
	{"L18", "a member can reassign a lapsed work order", checkReassign},
	{"L19", "the log replays a whole lease history", checkLeaseReplay},
	{"L20", "the revision stays first through every lease statement", checkLeaseStatementOrder},
	{"L21", "the state table decides every move", checkMovesFollowTheTable},
	{"L22", "a retry of a lease step changes nothing", checkLeaseRetries},
	{"L23", "a fenced write retried after a lost answer stores one event", checkWriteRetry},
	{"L24", "a renewal after the deadline is refused, so a lapse goes on record", checkRenewalAfterDeadline},
	{"L25", "a lane's outcome moves the work order and ends the claim", checkLaneOutcomes},
	{"L26", "the old holder cannot write after its lane ended", checkWriteAfterLaneEnd},
	{"L27", "a lane ends once", checkLaneEndsOnce},
	{"L28", "only the current generation can end a lane", checkStaleGenerationEnd},
	{"L29", "a lapsed lane ends after its worker returns", checkLapsedLaneEnd},
	{"L30", "a failed lane returns to ready while attempts remain", checkAttempts},
	{"L31", "a dispatch needs a claim and names one lane", checkDispatch},
	{"L32", "the per-repository and per-worker caps hold under racing claims", checkCaps},
	{"L33", "a lapsed work order counts toward every cap", checkLapsedCounts},
}

// Ready creates a work order and walks it to `ready` through the state table.
func (r *Rig) Ready(s ports.Store) (string, error) {
	order, err := r.NewWorkOrder(s)
	if err != nil {
		return "", err
	}
	ops := r.NewOps(s)
	for _, trigger := range []workorders.Trigger{
		workorders.TriggerAccepted, workorders.TriggerContractSubmitted, workorders.TriggerReadyPassed,
	} {
		if err := ops.Move(r.Ctx, r.Scope, order.ID, trigger, r.Actor); err != nil {
			return "", err
		}
	}
	return order.ID, nil
}

// Request builds a claim request with a new claim ID.
func (r *Rig) Request(id, worker string) workorders.ClaimRequest {
	return workorders.ClaimRequest{WorkOrder: id, ClaimID: r.ID("claim"), Worker: worker, TTLMs: TTLMs}
}

func (r *Rig) worker(id string) ledger.Actor {
	return ledger.Actor{Kind: ledger.ActorWorker, ID: id}
}

// Running claims a work order and starts its lane.
func (r *Rig) Running(s ports.Store, id, worker string) (workorders.Holder, error) {
	ops := r.NewOps(s)
	grant, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, worker), r.worker(worker))
	if err != nil {
		return workorders.Holder{}, err
	}
	lane := r.ID("lane")
	generation, err := ops.StartLane(r.Ctx, r.Scope, grant.Lease, lane, r.worker(worker))
	if err != nil {
		return workorders.Holder{}, err
	}
	return workorders.Holder{Lease: grant.Lease, Lane: lane, Generation: generation}, nil
}

// Expire moves database time past every lease and sweeps.
func (r *Rig) Expire(s ports.Store) error {
	r.DB.ShiftClock(TTLMs + 1)
	_, err := r.NewOps(s).Sweep(r.Ctx, r.Scope, r.Actor)
	return err
}

// WorkOrder reads one work order.
func (r *Rig) WorkOrder(s ports.Store, id string) (workorders.WorkOrder, error) {
	var w workorders.WorkOrder
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		w, err = tx.WorkOrders().Get(id)
		return err
	})
	return w, err
}

// Clock reads database time.
func (r *Rig) Clock(s ports.Store) (int64, error) {
	var now int64
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		now, err = tx.Now()
		return err
	})
	return now, err
}

func (r *Rig) countEvents(s ports.Store, eventType, subject string) (int, error) {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range events {
		if e.Type == eventType && e.Subject.ID == subject {
			count++
		}
	}
	return count, nil
}

var grantEvent = workorders.EventType(workorders.TriggerClaimGranted)

func checkClaimRace(r *Rig) error {
	s := r.Store()
	const rounds, racers = 25, 12
	for round := range rounds {
		id, err := r.Ready(s)
		if err != nil {
			return err
		}
		var winners atomic.Int64
		err = parallel(racers, func(i int) error {
			worker := fmt.Sprintf("worker-%d", i)
			_, err := r.NewOps(s).Claim(r.Ctx, r.Scope, r.Request(id, worker), r.worker(worker))
			if errors.Is(err, workorders.ErrLost) {
				return nil
			}
			if err == nil {
				winners.Add(1)
			}
			return err
		})
		if err != nil {
			return err
		}
		if n := winners.Load(); n != 1 {
			return Violatedf("round %d: %d workers won one work order", round, n)
		}
		if n, err := r.countEvents(s, grantEvent, id); err != nil {
			return err
		} else if n != 1 {
			return Violatedf("round %d: %d grants in the log", round, n)
		}
	}
	return nil
}

func checkFenceIncreases(r *Rig) error {
	s := r.Store()
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	seen := map[int64]bool{}
	var last int64
	for cycle := range 12 {
		ops := r.NewOps(s)
		grant, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, "worker"), r.worker("worker"))
		if err != nil {
			return fmt.Errorf("cycle %d: %w", cycle, err)
		}
		if seen[grant.Fence] || grant.Fence <= last {
			return Violatedf("cycle %d: fence %d after %d", cycle, grant.Fence, last)
		}
		seen[grant.Fence], last = true, grant.Fence
		if cycle%2 == 0 {
			if err := ops.Release(r.Ctx, r.Scope, grant.Lease, r.worker("worker")); err != nil {
				return err
			}
		} else {
			if _, err := ops.StartLane(r.Ctx, r.Scope, grant.Lease, r.ID("lane"), r.worker("worker")); err != nil {
				return err
			}
			if err := r.Expire(s); err != nil {
				return err
			}
			if err := ops.Reassign(r.Ctx, r.Scope, id, "cycle", r.Actor); err != nil {
				return err
			}
		}
		// A restart keeps only what the database holds.
		if r.DB.Restartable {
			if err := s.Close(); err != nil {
				return err
			}
			s = r.Store()
		}
	}
	return nil
}

func checkStaleWrite(r *Rig) error {
	s := r.Store()
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	old, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	if err := r.NewOps(s).Reassign(r.Ctx, r.Scope, id, "stale write", r.Actor); err != nil {
		return err
	}
	current, err := r.Running(s, id, "b")
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	_, err = ops.Write(r.Ctx, r.Scope, old, r.ID("write"), []byte("stale"), r.worker("a"))
	var fenced *workorders.FencedError
	if !errors.As(err, &fenced) {
		return Violatedf("a write with fence %d was not fenced: %v", old.Fence, err)
	}
	if fenced.Current != current.Fence {
		return Violatedf("the rejection named fence %d, the current one is %d", fenced.Current, current.Fence)
	}
	if after, err := r.Revision(s, r.Scope); err != nil {
		return err
	} else if after != before {
		return Violatedf("a rejected write moved the revision")
	}
	ahead := current
	ahead.Fence = current.Fence + 1
	if _, err := ops.Write(r.Ctx, r.Scope, ahead, r.ID("write"), []byte("ahead"), r.worker("b")); !errors.As(err, &fenced) {
		return Violatedf("a write with a fence from the future was accepted: %v", err)
	}
	_, err = ops.Write(r.Ctx, r.Scope, current, r.ID("write"), []byte("current"), r.worker("b"))
	return err
}

// checkInFlightWrite stops a write after its check, runs a whole handover, and
// lets the write finish. It may commit only if it is ordered before the handover,
// which the workspace revisions show.
func checkInFlightWrite(r *Rig) error {
	paused, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a := r.StoreWith(func(op, name string) {
		if op == "fenced_write" && name == StepGuarded {
			once.Do(func() {
				close(paused)
				<-resume
			})
		}
	})
	b := r.Store()
	id, err := r.Ready(b)
	if err != nil {
		return err
	}
	holder, err := r.Running(b, id, "a")
	if err != nil {
		return err
	}
	if err := r.Expire(b); err != nil {
		return err
	}
	type outcome struct {
		rev int64
		err error
	}
	written := make(chan outcome, 1)
	go func() {
		event, err := r.NewOps(a).Write(r.Ctx, r.Scope, holder, r.ID("write"), []byte("in flight"), r.worker("a"))
		written <- outcome{event.Rev, err}
	}()
	select {
	case <-paused:
	case o := <-written:
		return fmt.Errorf("the write finished without reaching its pause: %v", o.err)
	case <-time.After(10 * time.Second):
		return errors.New("the write never reached its pause")
	}
	claimed := make(chan outcome, 1)
	go func() {
		ops := r.NewOps(b)
		if err := ops.Reassign(r.Ctx, r.Scope, id, "handover", r.Actor); err != nil {
			claimed <- outcome{0, err}
			return
		}
		grant, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, "b"), r.worker("b"))
		claimed <- outcome{grant.Rev, err}
	}()
	var handover *outcome
	select {
	case o := <-claimed:
		handover = &o
	case <-time.After(250 * time.Millisecond):
		// The handover is waiting for the write, which is a correct order.
	}
	close(resume)
	write := <-written
	if handover == nil {
		o := <-claimed
		handover = &o
	}
	if handover.err != nil {
		return handover.err
	}
	var fenced *workorders.FencedError
	if errors.As(write.err, &fenced) {
		return nil
	}
	if write.err != nil {
		return write.err
	}
	if write.rev > handover.rev {
		return Violatedf("a stale write committed at rev %d after the grant at rev %d", write.rev, handover.rev)
	}
	return nil
}

// checkWriteAfterRevocation tries the old holder's token after each way ownership
// ends. It reports them all, so a variant shows each path can fail.
func checkWriteAfterRevocation(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	var accepted []string
	stale := func(path string, holder workorders.Holder) error {
		_, err := ops.Write(r.Ctx, r.Scope, holder, r.ID("write"), []byte(path), r.worker("a"))
		var fenced *workorders.FencedError
		switch {
		case errors.As(err, &fenced):
		case err == nil:
			accepted = append(accepted, path)
		default:
			return err
		}
		return nil
	}

	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	if err := ops.Reassign(r.Ctx, r.Scope, id, "laptop lost", r.Actor); err != nil {
		return err
	}
	if err := stale("reassignment", holder); err != nil {
		return err
	}

	if id, err = r.Ready(s); err != nil {
		return err
	}
	grant, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, "a"), r.worker("a"))
	if err != nil {
		return err
	}
	if err := ops.Release(r.Ctx, r.Scope, grant.Lease, r.worker("a")); err != nil {
		return err
	}
	if err := stale("release", workorders.Holder{Lease: grant.Lease}); err != nil {
		return err
	}

	parked, err := r.Parked(s, `{"human_resume":true}`)
	if err != nil {
		return err
	}
	if err := stale("parking", parked.Holder); err != nil {
		return err
	}
	if len(accepted) > 0 {
		return Violatedf("the old holder's write was accepted after %v, before the next claim", accepted)
	}
	return nil
}

func checkLapseNotTransfer(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	if ids, err := ops.Sweep(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	} else if len(ids) != 0 {
		return Violatedf("a live lease was swept")
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	w, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if w.State != workorders.Lapsed {
		return Violatedf("an expired lease is %s, want lapsed", w.State)
	}
	if w.LeaseWorker != "a" || w.Fence != holder.Fence {
		return Violatedf("a lapse changed the holder: worker %q fence %d", w.LeaseWorker, w.Fence)
	}
	if _, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, "b"), r.worker("b")); !errors.Is(err, workorders.ErrLost) {
		return Violatedf("a rival claimed a lapsed work order: %v", err)
	}
	return nil
}

func checkRenewCannotRevive(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	old, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	if err := ops.Renew(r.Ctx, r.Scope, old.Lease, TTLMs); err != nil {
		return fmt.Errorf("renewing a live lease: %w", err)
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	if err := ops.Renew(r.Ctx, r.Scope, old.Lease, TTLMs); !errors.Is(err, workorders.ErrLapsed) {
		return Violatedf("renewing a lapsed lease returned %v", err)
	}
	if err := ops.Reassign(r.Ctx, r.Scope, id, "revive", r.Actor); err != nil {
		return err
	}
	var fenced *workorders.FencedError
	if err := ops.Renew(r.Ctx, r.Scope, old.Lease, TTLMs); !errors.As(err, &fenced) {
		return Violatedf("renewing after a reassignment returned %v", err)
	}
	if w, err := r.WorkOrder(s, id); err != nil {
		return err
	} else if w.LeaseUntil != 0 {
		return Violatedf("a renewal after a reassignment set a lease on a ready work order")
	}
	current, err := r.Running(s, id, "b")
	if err != nil {
		return err
	}
	before, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(1000)
	if err := ops.Renew(r.Ctx, r.Scope, old.Lease, TTLMs); !errors.As(err, &fenced) {
		return Violatedf("renewing fence %d after a new grant returned %v", old.Fence, err)
	}
	after, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if after.LeaseUntil != before.LeaseUntil || after.Fence != current.Fence {
		return Violatedf("a stale renewal moved the new lease")
	}
	return nil
}

// checkStaleSweep holds one sweep between its read and its lapse while a second sweep
// lapses the lease and the holder returns. The list the first sweep read is out of
// date by then, so only the expiry predicate in the statement keeps the live lease.
func checkStaleSweep(r *Rig) error {
	paused, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	sweeper := r.StoreWith(func(op, name string) {
		if op == "lapse" && name == StepBefore {
			once.Do(func() {
				close(paused)
				<-resume
			})
		}
	})
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	r.DB.ShiftClock(TTLMs + 1)
	swept := make(chan error, 1)
	go func() {
		_, err := r.NewOps(sweeper).Sweep(r.Ctx, r.Scope, r.Actor)
		swept <- err
	}()
	select {
	case <-paused:
	case err := <-swept:
		return fmt.Errorf("the sweep finished without selecting: %v", err)
	case <-time.After(10 * time.Second):
		return errors.New("the sweep never reached its pause")
	}
	_, err = ops.Sweep(r.Ctx, r.Scope, r.Actor)
	if err == nil {
		err = ops.Return(r.Ctx, r.Scope, holder.Lease, TTLMs, r.worker("a"))
	}
	close(resume)
	if err != nil {
		return err
	}
	if err := <-swept; err != nil {
		return err
	}
	w, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if w.State != workorders.Running {
		return Violatedf("a returned lease was lapsed by a sweep that read it earlier: state %s", w.State)
	}
	return nil
}

// checkRenewalAfterDeadline covers a renewal that arrives after the deadline and
// before the sweep, as the first heartbeat after a machine wakes does. It leaves the
// row alone and does not fence the holder, the sweep records the lapse, and the same
// holder returns.
func checkRenewalAfterDeadline(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	before, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(TTLMs + 1)
	if err := ops.Renew(r.Ctx, r.Scope, holder.Lease, TTLMs); !errors.Is(err, workorders.ErrExpired) {
		return Violatedf("a renewal after the deadline returned %v", err)
	}
	after, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if after.State != workorders.Running || after.LeaseUntil != before.LeaseUntil {
		return Violatedf("a refused renewal changed the row: state %s, deadline %d", after.State, after.LeaseUntil)
	}
	lapsed, err := ops.Sweep(r.Ctx, r.Scope, r.Actor)
	if err != nil {
		return err
	}
	if len(lapsed) != 1 || lapsed[0] != id {
		return Violatedf("the sweep after a refused renewal lapsed %v", lapsed)
	}
	if err := ops.Renew(r.Ctx, r.Scope, holder.Lease, TTLMs); !errors.Is(err, workorders.ErrLapsed) {
		return Violatedf("a renewal after the lapse returned %v", err)
	}
	if err := ops.Return(r.Ctx, r.Scope, holder.Lease, TTLMs, r.worker("a")); err != nil {
		return fmt.Errorf("return: %w", err)
	}
	returned, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if returned.State != workorders.Running || returned.Fence != holder.Fence {
		return Violatedf("after the return: state %s, fence %d", returned.State, returned.Fence)
	}
	lapses, err := r.countEvents(s, workorders.EventType(workorders.TriggerLeaseExpired), id)
	if err != nil {
		return err
	}
	returns, err := r.countEvents(s, workorders.EventType(workorders.TriggerReturnedStarted), id)
	if err != nil {
		return err
	}
	if lapses != 1 || returns != 1 {
		return Violatedf("%d lapses and %d returns on record", lapses, returns)
	}
	return nil
}

func laneResult(outcome workorders.Outcome, reason string) workorders.LaneResult {
	return workorders.LaneResult{Report: workorders.Report{Outcome: outcome, Reason: reason}}
}

func checkLaneOutcomes(r *Rig) error {
	tests := []struct {
		outcome workorders.Outcome
		trigger workorders.Trigger
		state   workorders.State
	}{
		{workorders.OutcomeCompleted, workorders.TriggerLaneCompleted, workorders.Acceptance},
		{workorders.OutcomeBlocked, workorders.TriggerLaneBlocked, workorders.Blocked},
		{workorders.OutcomeFailed, workorders.TriggerAttemptsExhausted, workorders.Failed},
	}
	s := r.Store()
	ops := r.NewOps(s)
	r.MaxAttempts = 1
	defer func() { r.MaxAttempts = 0 }()
	for _, tt := range tests {
		id, err := r.Ready(s)
		if err != nil {
			return err
		}
		holder, err := r.Running(s, id, "a")
		if err != nil {
			return err
		}
		if err := ops.EndLane(r.Ctx, r.Scope, holder, laneResult(tt.outcome, "why"), r.worker("a")); err != nil {
			return fmt.Errorf("%s: %w", tt.outcome, err)
		}
		w, err := r.WorkOrder(s, id)
		if err != nil {
			return err
		}
		if w.State != tt.state || w.Fence <= holder.Fence || w.ClaimID != "" || w.LeaseWorker != "" || w.LeaseUntil != 0 {
			return Violatedf("after %s: %+v", tt.outcome, w)
		}
		count, err := r.countEvents(s, workorders.EventType(tt.trigger), id)
		if err != nil {
			return err
		}
		if count != 1 {
			return Violatedf("%d %s events", count, tt.trigger)
		}
		var claim workorders.Claim
		err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
			var err error
			claim, err = tx.Claims().Find(holder.ClaimID)
			return err
		})
		if err != nil {
			return err
		}
		if !claim.Ended() || claim.EndReason != string(tt.outcome) {
			return Violatedf("the claim after %s: %+v", tt.outcome, claim)
		}
	}
	return r.CheckProjection(s)
}

func checkWriteAfterLaneEnd(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	if err := ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeCompleted, ""), r.worker("a")); err != nil {
		return err
	}
	var fenced *workorders.FencedError
	if _, err := ops.Write(r.Ctx, r.Scope, holder, r.ID("write"), []byte("late"), r.worker("a")); !errors.As(err, &fenced) {
		return Violatedf("a write after the lane ended returned %v", err)
	}
	if err := ops.Renew(r.Ctx, r.Scope, holder.Lease, TTLMs); !errors.As(err, &fenced) {
		return Violatedf("a renewal after the lane ended returned %v", err)
	}
	return nil
}

func checkLaneEndsOnce(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	if err := ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeBlocked, "first"), r.worker("a")); err != nil {
		return err
	}
	var fenced *workorders.FencedError
	if err := ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeCompleted, ""), r.worker("a")); !errors.As(err, &fenced) {
		return Violatedf("a second outcome returned %v", err)
	}
	w, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if w.State != workorders.Blocked {
		return Violatedf("a second outcome moved the work order to %s", w.State)
	}
	again, err := r.Ready(s)
	if err != nil {
		return err
	}
	old, err := r.Running(s, again, "a")
	if err != nil {
		return err
	}
	if err := ops.EndLane(r.Ctx, r.Scope, old, laneResult(workorders.OutcomeFailed, workorders.ReasonCrashed), r.worker("a")); err != nil {
		return err
	}
	if _, err := r.Running(s, again, "b"); err != nil {
		return err
	}
	if err := ops.EndLane(r.Ctx, r.Scope, old, laneResult(workorders.OutcomeCompleted, ""), r.worker("a")); !errors.As(err, &fenced) {
		return Violatedf("an old holder's outcome on a rival's lane returned %v", err)
	}
	current, err := r.WorkOrder(s, again)
	if err != nil {
		return err
	}
	if current.State != workorders.Running {
		return Violatedf("an old holder ended a rival's lane: %s", current.State)
	}
	return nil
}

func checkStaleGenerationEnd(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	next, err := ops.Handoff(r.Ctx, r.Scope, holder, r.worker("a"))
	if err != nil {
		return err
	}
	var fenced *workorders.FencedError
	if err := ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeCompleted, ""), r.worker("a")); !errors.As(err, &fenced) {
		return Violatedf("the previous generation's outcome returned %v", err)
	}
	if w, err := r.WorkOrder(s, id); err != nil {
		return err
	} else if w.State != workorders.Running {
		return Violatedf("the previous generation ended the lane: %s", w.State)
	}
	holder.Generation = next
	return ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeCompleted, ""), r.worker("a"))
}

func checkLapsedLaneEnd(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	if err := ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeCompleted, ""), r.worker("a")); !errors.Is(err, workorders.ErrLapsed) {
		return Violatedf("an outcome on a lapsed lease returned %v", err)
	}
	if err := ops.Return(r.Ctx, r.Scope, holder.Lease, TTLMs, r.worker("a")); err != nil {
		return err
	}
	return ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeCompleted, ""), r.worker("a"))
}

func checkAttempts(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	r.MaxAttempts = 2
	defer func() { r.MaxAttempts = 0 }()
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	first, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	if err := ops.EndLane(r.Ctx, r.Scope, first, laneResult(workorders.OutcomeFailed, workorders.ReasonCrashed), r.worker("a")); err != nil {
		return err
	}
	w, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if w.State != workorders.Ready || w.Attempts != 1 {
		return Violatedf("after a first failure: state %s, attempts %d", w.State, w.Attempts)
	}
	second, err := r.Running(s, id, "b")
	if err != nil {
		return err
	}
	if second.Fence <= first.Fence {
		return Violatedf("a retry at fence %d after fence %d", second.Fence, first.Fence)
	}
	if err := ops.EndLane(r.Ctx, r.Scope, second, laneResult(workorders.OutcomeFailed, workorders.ReasonCrashed), r.worker("b")); err != nil {
		return err
	}
	if w, err = r.WorkOrder(s, id); err != nil {
		return err
	}
	if w.State != workorders.Failed || w.Attempts != 2 {
		return Violatedf("after a last failure: state %s, attempts %d", w.State, w.Attempts)
	}
	return r.CheckProjection(s)
}

func checkDispatch(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	if _, err := ops.Dispatch(r.Ctx, r.Scope, id, "lane-a", r.Actor); !errors.Is(err, workorders.ErrIllegalMove) {
		return Violatedf("a dispatch of unclaimed work returned %v", err)
	}
	grant, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, "a"), r.worker("a"))
	if err != nil {
		return err
	}
	if _, err := ops.Dispatch(r.Ctx, r.Scope, id, "lane-a", r.worker("a")); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a worker dispatched: %v", err)
	}
	lease, err := ops.Dispatch(r.Ctx, r.Scope, id, "lane-a", r.Actor)
	if err != nil {
		return err
	}
	if lease != grant.Lease {
		return Violatedf("the dispatch names %+v", lease)
	}
	if _, err := ops.Dispatch(r.Ctx, r.Scope, id, "lane-a", r.Actor); err != nil {
		return Violatedf("a retry of a dispatch returned %v", err)
	}
	if _, err := ops.Dispatch(r.Ctx, r.Scope, id, "lane-b", r.Actor); !errors.Is(err, workorders.ErrIllegalMove) {
		return Violatedf("a second lane on one claim returned %v", err)
	}
	count, err := r.countEvents(s, workorders.EventLaneDispatched, id)
	if err != nil {
		return err
	}
	if count != 1 {
		return Violatedf("%d dispatch events", count)
	}
	var claim workorders.Claim
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		claim, err = tx.Claims().Find(grant.ClaimID)
		return err
	})
	if err != nil {
		return err
	}
	if claim.Lane != "lane-a" {
		return Violatedf("the claim names %q", claim.Lane)
	}
	return r.CheckProjection(s)
}

func checkLeaseTimeFromDatabase(r *Rig) error {
	s := r.Store()
	const hour = int64(time.Hour / time.Millisecond)
	r.DB.ShiftClock(hour)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	before, err := r.Clock(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	after, err := r.Clock(s)
	if err != nil {
		return err
	}
	w, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if w.LeaseUntil < before+TTLMs || w.LeaseUntil > after+TTLMs {
		return Violatedf("lease_until %d is outside database time [%d, %d]", w.LeaseUntil, before+TTLMs, after+TTLMs)
	}
	// Only database time moved, so only database time can expire this lease.
	if err := r.Expire(s); err != nil {
		return err
	}
	if got, err := r.WorkOrder(s, id); err != nil {
		return err
	} else if got.State != workorders.Lapsed {
		return Violatedf("a lease did not expire on database time: state %s", got.State)
	}
	// A clock that steps back cannot undo what the fence settled.
	r.DB.ShiftClock(-hour)
	return r.NewOps(s).Return(r.Ctx, r.Scope, holder.Lease, TTLMs, r.worker("a"))
}

func checkClaimRetry(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	request := r.Request(id, "a")
	first, err := ops.Claim(r.Ctx, r.Scope, request, r.worker("a"))
	if err != nil {
		return err
	}
	base, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	retry, err := ops.Claim(r.Ctx, r.Scope, request, r.worker("a"))
	if errors.Is(err, workorders.ErrLost) {
		return Violatedf("a retry with the same claim_id was told it lost")
	}
	if err != nil {
		return err
	}
	if retry.Fence != first.Fence || retry.ClaimID != first.ClaimID || !retry.Replayed {
		return Violatedf("a retry got %+v, the original was %+v", retry, first)
	}
	if rev, err := r.Revision(s, r.Scope); err != nil {
		return err
	} else if rev != base {
		return Violatedf("a retry moved the revision from %d to %d", base, rev)
	}
	if n, err := r.countEvents(s, grantEvent, id); err != nil {
		return err
	} else if n != 1 {
		return Violatedf("a retry produced %d grants", n)
	}
	if _, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, "b"), r.worker("b")); !errors.Is(err, workorders.ErrLost) {
		return Violatedf("a rival claim returned %v", err)
	}
	if err := ops.Release(r.Ctx, r.Scope, first.Lease, r.worker("a")); err != nil {
		return err
	}
	if _, err := ops.Claim(r.Ctx, r.Scope, request, r.worker("a")); !errors.Is(err, workorders.ErrLost) {
		return Violatedf("a retry after the release granted again: %v", err)
	}
	return nil
}

func checkClaimedLapses(r *Rig) error {
	s := r.Store()
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	if _, err := r.NewOps(s).Claim(r.Ctx, r.Scope, r.Request(id, "a"), r.worker("a")); err != nil {
		return err
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	w, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if w.State != workorders.Lapsed {
		return Violatedf("a worker died in claimed, and its expired lease stays %s with no path out", w.State)
	}
	return nil
}

func checkReturn(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	if err := ops.Return(r.Ctx, r.Scope, holder.Lease, TTLMs, r.worker("a")); err != nil {
		return Violatedf("a worker with the current fence could not return: %v", err)
	}
	w, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	now, err := r.Clock(s)
	if err != nil {
		return err
	}
	if w.State != workorders.Running || w.Fence != holder.Fence || w.LeaseUntil <= now {
		return Violatedf("a return left state %s fence %d lease_until %d", w.State, w.Fence, w.LeaseUntil)
	}
	if err := ops.Renew(r.Ctx, r.Scope, holder.Lease, TTLMs); err != nil {
		return err
	}
	if _, err := ops.Write(r.Ctx, r.Scope, holder, r.ID("write"), []byte("back"), r.worker("a")); err != nil {
		return err
	}

	// A lease that lapsed before its lane started must return to claimed, or the
	// work order would be running with no lane.
	early, err := r.Ready(s)
	if err != nil {
		return err
	}
	grant, err := ops.Claim(r.Ctx, r.Scope, r.Request(early, "a"), r.worker("a"))
	if err != nil {
		return err
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	if err := ops.Return(r.Ctx, r.Scope, grant.Lease, TTLMs, r.worker("a")); err != nil {
		return Violatedf("a worker that lapsed before starting could not return: %v", err)
	}
	if w, err = r.WorkOrder(s, early); err != nil {
		return err
	}
	if w.State != workorders.Claimed {
		return Violatedf("a return from an unstarted lapse left state %s, want claimed", w.State)
	}
	_, err = ops.StartLane(r.Ctx, r.Scope, grant.Lease, r.ID("lane"), r.worker("a"))
	return err
}

func checkReturnRacesReassign(r *Rig) error {
	s := r.Store()
	const rounds = 20
	for round := range rounds {
		id, err := r.Ready(s)
		if err != nil {
			return err
		}
		holder, err := r.Running(s, id, "a")
		if err != nil {
			return err
		}
		if err := r.Expire(s); err != nil {
			return err
		}
		var returned, moved atomic.Int64
		err = parallel(2, func(i int) error {
			ops := r.NewOps(s)
			if i == 0 {
				err := ops.Return(r.Ctx, r.Scope, holder.Lease, TTLMs, r.worker("a"))
				var fenced *workorders.FencedError
				if errors.As(err, &fenced) {
					return nil
				}
				if err == nil {
					returned.Add(1)
				}
				return err
			}
			err := ops.Reassign(r.Ctx, r.Scope, id, "race", r.Actor)
			if errors.Is(err, workorders.ErrNotLapsed) {
				return nil
			}
			if err == nil {
				moved.Add(1)
			}
			return err
		})
		if err != nil {
			return err
		}
		if returned.Load()+moved.Load() != 1 {
			return Violatedf("round %d: %d returns and %d reassignments", round, returned.Load(), moved.Load())
		}
	}
	return nil
}

func checkEligibility(r *Rig) error {
	const limit, racers = 2, 8
	gate := newBarrier(racers)
	s := r.StoreWith(func(op, name string) {
		if op == "claim" && name == StepEligible {
			gate.wait(60 * time.Millisecond)
		}
	})
	limited := ledger.WorkspaceID("workspace-limited")
	if _, err := s.CreateWorkspace(r.Ctx, ledger.Workspace{ID: limited, WIPLimit: limit}, r.Actor); err != nil {
		return err
	}
	ids := make([]string, racers)
	saved := r.Scope
	r.Scope = limited
	for i := range ids {
		id, err := r.Ready(s)
		if err != nil {
			return err
		}
		ids[i] = id
	}
	r.Scope = saved
	var granted atomic.Int64
	err := parallel(racers, func(i int) error {
		_, err := r.NewOps(s).Claim(r.Ctx, limited, r.Request(ids[i], "w"), r.worker("w"))
		if errors.Is(err, workorders.ErrIneligible) {
			return nil
		}
		if err == nil {
			granted.Add(1)
		}
		return err
	})
	if err != nil {
		return err
	}
	if n := granted.Load(); n > limit {
		return Violatedf("%d claims granted under a limit of %d", n, limit)
	}

	paused := ledger.WorkspaceID("workspace-paused")
	if _, err := s.CreateWorkspace(r.Ctx, ledger.Workspace{ID: paused, FleetMode: ledger.FleetPause}, r.Actor); err != nil {
		return err
	}
	r.Scope = paused
	id, err := r.Ready(s)
	r.Scope = saved
	if err != nil {
		return err
	}
	if _, err := r.NewOps(s).Claim(r.Ctx, paused, r.Request(id, "a"), r.worker("a")); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("a claim passed a paused fleet: %v", err)
	}
	return nil
}

// readyIn walks a work order of one repository to `ready`.
func (r *Rig) readyIn(s ports.Store, repository string) (string, error) {
	ops := r.NewOps(s)
	order, err := ops.Create(r.Ctx, r.Scope, workorders.WorkOrder{
		ID: r.ID("wo"), Kind: workorders.KindChange, Title: "Add the retry budget", RepositoryID: repository, MaxAttempts: defaultMaxAttempts,
	}, r.Actor)
	if err != nil {
		return "", err
	}
	for _, trigger := range []workorders.Trigger{
		workorders.TriggerAccepted, workorders.TriggerContractSubmitted, workorders.TriggerReadyPassed,
	} {
		if err := ops.Move(r.Ctx, r.Scope, order.ID, trigger, r.Actor); err != nil {
			return "", err
		}
	}
	return order.ID, nil
}

// raceUnder sends racers claims at one cap, and reports how many won.
func (r *Rig) raceUnder(s ports.Store, scope ledger.WorkspaceID, ids []string, worker func(int) string) (int64, error) {
	var granted atomic.Int64
	err := parallel(len(ids), func(i int) error {
		_, err := r.NewOps(s).Claim(r.Ctx, scope, r.Request(ids[i], worker(i)), r.worker(worker(i)))
		if errors.Is(err, workorders.ErrIneligible) {
			return nil
		}
		if err == nil {
			granted.Add(1)
		}
		return err
	})
	return granted.Load(), err
}

func checkCaps(r *Rig) error {
	const racers, limit = 8, 2
	gate := newBarrier(racers)
	s := r.StoreWith(func(op, name string) {
		if op == "claim" && name == StepEligible {
			gate.wait(40 * time.Millisecond)
		}
	})
	saved := r.Scope
	defer func() { r.Scope = saved }()
	repositories := ledger.WorkspaceID("workspace-repositories")
	if _, err := s.CreateWorkspace(r.Ctx, ledger.Workspace{ID: repositories, RepositoryLimit: limit}, r.Actor); err != nil {
		return err
	}
	r.Scope = repositories
	ids := make([]string, racers)
	for i := range ids {
		id, err := r.readyIn(s, "repository-one")
		if err != nil {
			return err
		}
		ids[i] = id
	}
	other, err := r.readyIn(s, "repository-two")
	if err != nil {
		return err
	}
	granted, err := r.raceUnder(s, repositories, ids, func(i int) string { return fmt.Sprint("w", i) })
	if err != nil {
		return err
	}
	if granted > limit {
		return Violatedf("%d claims granted in a repository with a cap of %d", granted, limit)
	}
	if _, err := r.NewOps(s).Claim(r.Ctx, repositories, r.Request(other, "w"), r.worker("w")); err != nil {
		return Violatedf("a cap on one repository refused another: %v", err)
	}

	workers := ledger.WorkspaceID("workspace-workers")
	if _, err := s.CreateWorkspace(r.Ctx, ledger.Workspace{ID: workers, WorkerLimit: limit}, r.Actor); err != nil {
		return err
	}
	r.Scope = workers
	for i := range ids {
		id, err := r.Ready(s)
		if err != nil {
			return err
		}
		ids[i] = id
	}
	spare, err := r.Ready(s)
	if err != nil {
		return err
	}
	granted, err = r.raceUnder(s, workers, ids, func(int) string { return "one" })
	if err != nil {
		return err
	}
	if granted > limit {
		return Violatedf("%d claims granted to a worker with a cap of %d", granted, limit)
	}
	if _, err := r.NewOps(s).Claim(r.Ctx, workers, r.Request(spare, "two"), r.worker("two")); err != nil {
		return Violatedf("a cap on one worker refused another: %v", err)
	}
	return nil
}

func checkLapsedCounts(r *Rig) error {
	s := r.Store()
	saved := r.Scope
	defer func() { r.Scope = saved }()
	scope := ledger.WorkspaceID("workspace-lapsed")
	if _, err := s.CreateWorkspace(r.Ctx, ledger.Workspace{ID: scope, WorkerLimit: 1, WIPLimit: 1}, r.Actor); err != nil {
		return err
	}
	r.Scope = scope
	first, err := r.Ready(s)
	if err != nil {
		return err
	}
	second, err := r.Ready(s)
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	if _, err := ops.Claim(r.Ctx, scope, r.Request(first, "a"), r.worker("a")); err != nil {
		return err
	}
	r.DB.ShiftClock(TTLMs + 1)
	if _, err := ops.Sweep(r.Ctx, scope, r.Actor); err != nil {
		return err
	}
	if got, err := r.WorkOrder(s, first); err != nil || got.State != workorders.Lapsed {
		return fmt.Errorf("the first claim did not lapse: %+v: %v", got, err)
	}
	if _, err := ops.Claim(r.Ctx, scope, r.Request(second, "a"), r.worker("a")); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("a lapsed work order did not count: %v", err)
	}
	return nil
}

func checkGeneration(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	next, err := ops.Handoff(r.Ctx, r.Scope, holder, r.worker("a"))
	if err != nil {
		return err
	}
	if next <= holder.Generation {
		return fmt.Errorf("a handoff kept generation %d", next)
	}
	var fenced *workorders.FencedError
	if _, err := ops.Write(r.Ctx, r.Scope, holder, r.ID("write"), []byte("old"), r.worker("a")); !errors.As(err, &fenced) {
		return Violatedf("the previous generation wrote after a handoff: %v", err)
	}
	holder.Generation = next
	_, err = ops.Write(r.Ctx, r.Scope, holder, r.ID("write"), []byte("new"), r.worker("a"))
	return err
}

func checkRenewalAddsNothing(r *Rig) error {
	s := r.Store()
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	base, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	before, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(1000)
	if err := r.NewOps(s).Renew(r.Ctx, r.Scope, holder.Lease, TTLMs); err != nil {
		return err
	}
	after, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if after.LeaseUntil <= before.LeaseUntil {
		return Violatedf("a renewal did not extend the lease: %d after %d", after.LeaseUntil, before.LeaseUntil)
	}
	rev, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if rev != base {
		return Violatedf("a renewal moved the revision from %d to %d", base, rev)
	}
	return r.CheckRevisions(s, r.Scope)
}

func checkReassign(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	if err := ops.Reassign(r.Ctx, r.Scope, id, "too soon", r.Actor); !errors.Is(err, workorders.ErrNotLapsed) {
		return Violatedf("reassigning a ready work order returned %v", err)
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	if err := ops.Reassign(r.Ctx, r.Scope, id, "live lease", r.Actor); !errors.Is(err, workorders.ErrNotLapsed) {
		return Violatedf("reassigning a live lease returned %v", err)
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	if err := ops.Reassign(r.Ctx, r.Scope, id, "stolen", r.worker("b")); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a worker reassigned a work order: %v", err)
	}
	if err := ops.Reassign(r.Ctx, r.Scope, id, "laptop lost", r.Actor); err != nil {
		return err
	}
	w, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if w.State != workorders.Ready || w.Fence <= holder.Fence || w.ClaimID != "" || w.LeaseWorker != "" || w.LeaseUntil != 0 {
		return Violatedf("a reassignment left %+v", w)
	}
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	last := events[len(events)-1]
	if last.Actor != r.Actor || last.Type != workorders.EventType(workorders.TriggerTakeover) {
		return Violatedf("a reassignment recorded %+v", last)
	}
	if !strings.Contains(string(last.Data), "laptop lost") {
		return Violatedf("a reassignment lost its reason: %s", last.Data)
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		claim, err := tx.Claims().Find(holder.ClaimID)
		if err != nil {
			return err
		}
		if !claim.Ended() || claim.EndReason != workorders.EndReassigned {
			return Violatedf("the reassigned claim is %+v", claim)
		}
		return nil
	})
}

func checkLeaseReplay(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	if _, err := ops.Write(r.Ctx, r.Scope, holder, r.ID("write"), []byte("one"), r.worker("a")); err != nil {
		return err
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	if err := ops.Return(r.Ctx, r.Scope, holder.Lease, TTLMs, r.worker("a")); err != nil {
		return err
	}
	if err := ops.Renew(r.Ctx, r.Scope, holder.Lease, TTLMs); err != nil {
		return err
	}
	next, err := ops.Handoff(r.Ctx, r.Scope, holder, r.worker("a"))
	if err != nil {
		return err
	}
	holder.Generation = next
	if _, err := ops.Write(r.Ctx, r.Scope, holder, r.ID("write"), []byte("two"), r.worker("a")); err != nil {
		return err
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	w, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if w.Rev != 3 {
		return Violatedf("work order rev %d after three guarded writes", w.Rev)
	}
	r.DB.ShiftClock(1000)
	if err := ops.Renew(r.Ctx, r.Scope, holder.Lease, TTLMs); err != nil {
		return err
	}
	renewed, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if err := ops.Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	rebuilt, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if rebuilt.LeaseUntil != renewed.LeaseUntil {
		return Violatedf("a rebuild moved a live lease from %d to %d", renewed.LeaseUntil, rebuilt.LeaseUntil)
	}
	return r.CheckProjection(s)
}

func checkLeaseStatementOrder(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	if err := ops.Renew(r.Ctx, r.Scope, holder.Lease, TTLMs); err != nil {
		return err
	}
	if _, err := ops.Write(r.Ctx, r.Scope, holder, r.ID("write"), []byte("traced"), r.worker("a")); err != nil {
		return err
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	if err := ops.Return(r.Ctx, r.Scope, holder.Lease, TTLMs, r.worker("a")); err != nil {
		return err
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	if err := ops.Reassign(r.Ctx, r.Scope, id, "traced", r.Actor); err != nil {
		return err
	}
	count, err := CheckRevisionFirst(r.trace.Entries())
	if err != nil {
		return err
	}
	if count < 8 {
		return fmt.Errorf("the trace held only %d write transactions", count)
	}
	return nil
}

func checkMovesFollowTheTable(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	order, err := r.NewWorkOrder(s)
	if err != nil {
		return err
	}
	if err := ops.Move(r.Ctx, r.Scope, order.ID, workorders.TriggerReadyPassed, r.Actor); !errors.Is(err, workorders.ErrIllegalMove) {
		return Violatedf("intake skipped to ready: %v", err)
	}
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	if err := ops.Move(r.Ctx, r.Scope, id, workorders.TriggerClaimGranted, r.Actor); !errors.Is(err, workorders.ErrLeasedEdge) {
		return Violatedf("a move took the claim edge: %v", err)
	}
	if err := ops.Move(r.Ctx, r.Scope, id, workorders.TriggerBackToReview, r.Actor); err != nil {
		return err
	}
	if err := ops.Cancel(r.Ctx, r.Scope, id, "no longer wanted", r.Actor); err != nil {
		return err
	}
	if err := ops.Move(r.Ctx, r.Scope, id, workorders.TriggerReadyPassed, r.Actor); !errors.Is(err, workorders.ErrIllegalMove) {
		return Violatedf("a canceled work order moved: %v", err)
	}
	if _, err := ops.Claim(r.Ctx, r.Scope, r.Request("no-such-work-order", "a"), r.worker("a")); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a claim on an unknown work order returned %v", err)
	}
	return r.CheckProjection(s)
}

// checkLeaseRetries repeats each step whose answer a crash can lose.
func checkLeaseRetries(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	grant, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, "a"), r.worker("a"))
	if err != nil {
		return err
	}
	lane := r.ID("lane")
	first, err := ops.StartLane(r.Ctx, r.Scope, grant.Lease, lane, r.worker("a"))
	if err != nil {
		return err
	}
	again, err := ops.StartLane(r.Ctx, r.Scope, grant.Lease, lane, r.worker("a"))
	if err != nil {
		return Violatedf("a repeated lane start returned %v", err)
	}
	if again != first {
		return Violatedf("a repeated lane start returned generation %d, then %d", first, again)
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	for range 2 {
		if err := ops.Return(r.Ctx, r.Scope, grant.Lease, TTLMs, r.worker("a")); err != nil {
			return Violatedf("a repeated return returned %v", err)
		}
	}
	if n, err := r.countEvents(s, workorders.EventType(workorders.TriggerReturnedStarted), id); err != nil {
		return err
	} else if n != 1 {
		return Violatedf("a repeated return wrote %d events", n)
	}
	other, err := r.Ready(s)
	if err != nil {
		return err
	}
	second, err := ops.Claim(r.Ctx, r.Scope, r.Request(other, "a"), r.worker("a"))
	if err != nil {
		return err
	}
	for range 2 {
		if err := ops.Release(r.Ctx, r.Scope, second.Lease, r.worker("a")); err != nil {
			return Violatedf("a repeated release returned %v", err)
		}
	}
	if n, err := r.countEvents(s, workorders.EventType(workorders.TriggerClaimReleased), other); err != nil {
		return err
	} else if n != 1 {
		return Violatedf("a repeated release wrote %d events", n)
	}
	return r.CheckRevisions(s, r.Scope)
}

func checkWriteRetry(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	writeID := r.ID("write")
	first, err := ops.Write(r.Ctx, r.Scope, holder, writeID, []byte("once"), r.worker("a"))
	if err != nil {
		return err
	}
	before, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	again, err := ops.Write(r.Ctx, r.Scope, holder, writeID, []byte("once"), r.worker("a"))
	if err != nil {
		return Violatedf("a retry failed: %v", err)
	}
	if again.Rev != first.Rev {
		return Violatedf("the retry stored rev %d after rev %d", again.Rev, first.Rev)
	}
	count, err := r.countEvents(s, workorders.EventFencedWrite, id)
	if err != nil {
		return err
	}
	if count != 1 {
		return Violatedf("%d writes stored", count)
	}
	after, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if after.Rev != before.Rev {
		return Violatedf("the retry moved the write count from %d to %d", before.Rev, after.Rev)
	}
	if err := r.Expire(s); err != nil {
		return err
	}
	if err := ops.Reassign(r.Ctx, r.Scope, id, "lost", r.Actor); err != nil {
		return err
	}
	late, err := ops.Write(r.Ctx, r.Scope, holder, writeID, []byte("once"), r.worker("a"))
	if err != nil {
		return Violatedf("a retry after the fence moved failed: %v", err)
	}
	if late.Rev != first.Rev {
		return Violatedf("a late retry answered rev %d", late.Rev)
	}
	return r.CheckProjection(s)
}
