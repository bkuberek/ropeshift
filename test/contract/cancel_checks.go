package contract

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// CancelChecks are what canceling work promises: it ends the work from any state that is not terminal,
// in one transaction, and only a member can ask. A lane with a process is told through desired state and
// stays as it was last reported until its worker ends it, and a lane with no process ends at once.
var CancelChecks = []Check{
	{"CN1", "a member cancels work from every state that is not terminal", checkCancelEveryState},
	{"CN2", "canceling held work moves the fence and ends the claim", checkCancelEndsTheClaim},
	{"CN3", "the holder of canceled work can do nothing more", checkCanceledHolder},
	{"CN4", "a cancel retried after a lost answer changes nothing", checkCancelRetry},
	{"CN5", "work that finished stays finished", checkCancelTerminal},
	{"CN6", "only a member cancels work, and no other path leads there", checkOnlyMemberCancels},
	{"CN7", "a parked lane ends in the cancel, with its wake, generation, and messages", checkCancelParked},
	{"CN8", "a woken lane ends in the cancel, so nothing brings it back", checkCancelWoken},
	{"CN9", "a running lane reads as last reported until its worker ends it", checkCancelRunning},
	{"CN10", "only the worker that ran a lane can end it after a cancel", checkCancelStranger},
	{"CN11", "desired state stops naming canceled work", checkCancelDesired},
	{"CN12", "a cancel that races a lane's end or a return leaves one history", checkCancelRaces},
	{"CN13", "a cancel keeps its reason and its actor on record", checkCancelRecord},
	{"CN14", "a claim bringing a parked lane back ends that lane too", checkCancelResumeClaim},
}

// inState walks a new work order to a state through the paths that make it.
func (r *Rig) inState(s ports.Store, state workorders.State) (workorders.WorkOrder, error) {
	ops := r.NewOps(s)
	var id string
	var err error
	switch state {
	case workorders.Intake, workorders.Planning, workorders.ContractReview:
		order, err := r.NewWorkOrder(s)
		if err != nil {
			return order, err
		}
		id = order.ID
		if state != workorders.Intake {
			if err := ops.Move(r.Ctx, r.Scope, id, workorders.TriggerAccepted, r.Actor); err != nil {
				return order, err
			}
		}
		if state == workorders.ContractReview {
			if err := ops.Move(r.Ctx, r.Scope, id, workorders.TriggerContractSubmitted, r.Actor); err != nil {
				return order, err
			}
		}
	case workorders.Parked:
		now, err := r.Clock(s)
		if err != nil {
			return workorders.WorkOrder{}, err
		}
		request, err := r.Parked(s, atMs(now+aheadMs))
		if err != nil {
			return workorders.WorkOrder{}, err
		}
		id = request.WorkOrder
	default:
		if id, err = r.Ready(s); err != nil {
			return workorders.WorkOrder{}, err
		}
		if err := r.heldInState(s, id, state); err != nil {
			return workorders.WorkOrder{}, err
		}
	}
	return r.WorkOrder(s, id)
}

func (r *Rig) heldInState(s ports.Store, id string, state workorders.State) error {
	ops := r.NewOps(s)
	switch state {
	case workorders.Ready:
		return nil
	case workorders.Claimed:
		_, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, "a"), r.worker("a"))
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	switch state {
	case workorders.Lapsed:
		return r.Expire(s)
	case workorders.Acceptance:
		return ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeCompleted, ""), r.worker("a"))
	case workorders.Blocked:
		return ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeBlocked, "stuck"), r.worker("a"))
	}
	return nil
}

func checkCancelEveryState(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	for _, state := range workorders.States() {
		if workorders.Terminal(state) {
			continue
		}
		order, err := r.inState(s, state)
		if err != nil {
			return err
		}
		if order.State != state {
			return Violatedf("the walk reached %s, want %s", order.State, state)
		}
		if err := ops.Cancel(r.Ctx, r.Scope, order.ID, "no longer wanted", r.Actor); err != nil {
			return Violatedf("canceling %s work: %v", state, err)
		}
		after, err := r.WorkOrder(s, order.ID)
		if err != nil {
			return err
		}
		if after.State != workorders.Canceled || after.ClaimID != "" || after.LeaseWorker != "" || after.LeaseUntil != 0 || after.ResumeLaneID != "" {
			return Violatedf("canceling %s work left %+v", state, after)
		}
		if err := r.CheckProjection(s); err != nil {
			return Violatedf("after canceling %s work: %v", state, err)
		}
	}
	return nil
}

func checkCancelEndsTheClaim(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	for _, state := range []workorders.State{workorders.Claimed, workorders.Running, workorders.Lapsed} {
		order, err := r.inState(s, state)
		if err != nil {
			return err
		}
		if err := ops.Cancel(r.Ctx, r.Scope, order.ID, "dropped", r.Actor); err != nil {
			return err
		}
		after, err := r.WorkOrder(s, order.ID)
		if err != nil {
			return err
		}
		if after.Fence <= order.Fence {
			return Violatedf("canceling %s work left fence %d", state, after.Fence)
		}
		err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
			claim, err := tx.Claims().Find(order.ClaimID)
			if err != nil {
				return err
			}
			if !claim.Ended() || claim.EndReason != workorders.EndCanceled {
				return Violatedf("the canceled claim is %+v", claim)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// fencedBy names what a stale holder's step must return.
func fencedBy(step string, err error) error {
	var fenced *workorders.FencedError
	if !errors.As(err, &fenced) {
		return Violatedf("%s after a cancel returned %v", step, err)
	}
	if fenced.State != workorders.Canceled {
		return Violatedf("%s heard state %s", step, fenced.State)
	}
	return nil
}

func checkCanceledHolder(r *Rig) error {
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
	if err := ops.Cancel(r.Ctx, r.Scope, id, "dropped", r.Actor); err != nil {
		return err
	}
	a := r.worker("a")
	if _, err := ops.Write(r.Ctx, r.Scope, holder, r.ID("write"), []byte("late"), a); fencedBy("a write", err) != nil {
		return fencedBy("a write", err)
	}
	if err := ops.Renew(r.Ctx, r.Scope, holder.Lease, TTLMs); fencedBy("a renewal", err) != nil {
		return fencedBy("a renewal", err)
	}
	if err := ops.Return(r.Ctx, r.Scope, holder.Lease, TTLMs, a); fencedBy("a return", err) != nil {
		return fencedBy("a return", err)
	}
	if err := ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeCompleted, ""), a); fencedBy("a lane's end", err) != nil {
		return fencedBy("a lane's end", err)
	}
	if _, err := ops.Handoff(r.Ctx, r.Scope, holder, a); fencedBy("a handoff", err) != nil {
		return fencedBy("a handoff", err)
	}
	wake := parking.Draft{ID: r.ID("wake"), Expr: `{"human_resume":true}`}
	if err := ops.BeginParking(r.Ctx, r.Scope, holder, wake, a); fencedBy("parking", err) != nil {
		return fencedBy("parking", err)
	}
	if _, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, "b"), r.worker("b")); !errors.Is(err, workorders.ErrLost) {
		return Violatedf("a claim on canceled work returned %v", err)
	}
	if _, err := ops.Dispatch(r.Ctx, r.Scope, id, r.ID("lane"), r.Actor); !errors.Is(err, workorders.ErrIllegalMove) {
		return Violatedf("a dispatch on canceled work returned %v", err)
	}
	return r.CheckProjection(s)
}

func checkCancelRetry(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	order, err := r.inState(s, workorders.Running)
	if err != nil {
		return err
	}
	if err := ops.Cancel(r.Ctx, r.Scope, order.ID, "dropped", r.Actor); err != nil {
		return err
	}
	before, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	if err := ops.Cancel(r.Ctx, r.Scope, order.ID, "dropped", r.Actor); err != nil {
		return Violatedf("a retried cancel returned %v", err)
	}
	after, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	if len(after) != len(before) {
		return Violatedf("a retried cancel appended %d events", len(after)-len(before))
	}
	return nil
}

func checkCancelTerminal(r *Rig) error {
	r.MaxAttempts = 1
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
	if err := ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeFailed, "crashed"), r.worker("a")); err != nil {
		return err
	}
	if err := ops.Cancel(r.Ctx, r.Scope, id, "too late", r.Actor); !errors.Is(err, workorders.ErrIllegalMove) {
		return Violatedf("canceling failed work returned %v", err)
	}
	return hasOrderState(r, s, id, workorders.Failed)
}

func hasOrderState(r *Rig, s ports.Store, id string, want workorders.State) error {
	row, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if row.State != want {
		return Violatedf("%s reads %s, want %s", id, row.State, want)
	}
	return nil
}

func checkOnlyMemberCancels(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	order, err := r.inState(s, workorders.Running)
	if err != nil {
		return err
	}
	before, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"}, {Kind: ledger.ActorLane, ID: "lane-1"},
		{Kind: ledger.ActorWorker, ID: "a"}, {Kind: ledger.ActorSystem, ID: "tick"},
	} {
		if err := ops.Cancel(r.Ctx, r.Scope, order.ID, "not mine to end", actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s canceled work: %v", actor.Kind, err)
		}
		if err := ops.Move(r.Ctx, r.Scope, order.ID, workorders.TriggerCanceled, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s took the cancel edge: %v", actor.Kind, err)
		}
	}
	unheld, err := r.Ready(s)
	if err != nil {
		return err
	}
	if err := ops.Move(r.Ctx, r.Scope, unheld, workorders.TriggerCanceled, r.worker("a")); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a worker took the cancel edge of ready work: %v", err)
	}
	if err := ops.Move(r.Ctx, r.Scope, unheld, workorders.TriggerCanceled, r.Actor); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a move took the cancel edge, which would leave its lanes alone: %v", err)
	}
	after, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	for _, event := range after[len(before):] {
		if event.Type == workorders.EventType(workorders.TriggerCanceled) {
			return Violatedf("a refused actor left %+v", event)
		}
	}
	return hasOrderState(r, s, order.ID, workorders.Running)
}

// expectEnded holds a lane to what its end records.
func (s *statusRig) expectEnded(want string) error {
	view, err := s.plane.Lane(s.r.Ctx, s.lane)
	if err != nil {
		return err
	}
	if view.ActualState != ledger.StateEnded || view.Freshness != ledger.FreshnessObserved {
		return Violatedf("the lane reads %s, %s", view.ActualState, view.Freshness)
	}
	generations, err := s.generations()
	if err != nil {
		return err
	}
	last := generations[len(generations)-1]
	if last.EndedAt == 0 || last.EndReason != want {
		return Violatedf("its last generation is %+v, want it ended by %s", last, want)
	}
	return nil
}

func checkCancelParked(r *Rig) error {
	s, wake, err := parkedOn(r, true)
	if err != nil {
		return err
	}
	if err := r.NewOps(s.store).Cancel(r.Ctx, r.Scope, s.grant.WorkOrder, "dropped", r.Actor); err != nil {
		return err
	}
	if err := s.expectEnded(workorders.EndParked); err != nil {
		return err
	}
	fired, err := r.readWake(s.store, wake)
	if err != nil {
		return err
	}
	if fired.FiredBy != parking.RetiredCanceled {
		return Violatedf("the parked lane's wake reads %+v", fired)
	}
	if woke, err := r.NewOps(s.store).ResumeLane(r.Ctx, r.Scope, s.lane, r.Actor); err != nil || len(woke) != 0 {
		return Violatedf("a canceled lane woke: %v, %v", woke, err)
	}
	if err := countLaneEnds(r, s, 1); err != nil {
		return err
	}
	return r.CheckWakes(s.store)
}

// countLaneEnds holds a lane to one record of its end.
func countLaneEnds(r *Rig, s *statusRig, want int) error {
	events, err := r.Events(s.store, r.Scope)
	if err != nil {
		return err
	}
	got := 0
	for _, event := range events {
		if event.Type == ledger.EventLaneEnded && event.Subject.ID == s.lane {
			got++
		}
	}
	if got != want {
		return Violatedf("%d records of the lane's end, want %d", got, want)
	}
	return nil
}

func checkCancelWoken(r *Rig) error {
	s, err := wokenOn(r, true)
	if err != nil {
		return err
	}
	if err := r.NewOps(s.store).Cancel(r.Ctx, r.Scope, s.grant.WorkOrder, "dropped", r.Actor); err != nil {
		return err
	}
	if err := s.expectEnded(workorders.EndParked); err != nil {
		return err
	}
	state, err := s.desired()
	if err != nil {
		return err
	}
	if len(state.Woken) != 0 {
		return Violatedf("desired state offers %+v", state.Woken)
	}
	if _, err := s.claim(); !errors.Is(err, workorders.ErrLost) {
		return Violatedf("a claim on a canceled lane returned %v", err)
	}
	return r.CheckProjection(s.store)
}

// The session of a claim on woken work has not come back yet, so the lane has no process to stop.
func checkCancelResumeClaim(r *Rig) error {
	s, err := wokenOn(r, true)
	if err != nil {
		return err
	}
	grant, err := s.claim()
	if err != nil {
		return err
	}
	if err := r.NewOps(s.store).Cancel(r.Ctx, r.Scope, s.grant.WorkOrder, "dropped", r.Actor); err != nil {
		return err
	}
	if err := s.expectEnded(workorders.EndParked); err != nil {
		return err
	}
	reply, err := s.resumeUpload(grant, 2, ledger.StartResume)
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadFenced {
		return Violatedf("a resume after a cancel was answered %+v", reply.Rejected)
	}
	return s.expectEnded(workorders.EndParked)
}

func checkCancelRunning(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := accepted(s.report(s.working)); err != nil {
		return err
	}
	queued, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	if err := r.NewOps(s.store).Cancel(r.Ctx, r.Scope, s.grant.WorkOrder, "dropped", r.Actor); err != nil {
		return err
	}
	if _, err := s.want("working", ledger.FreshnessLastReported); err != nil {
		return err
	}
	if err := s.expectMessage(queued.ID, messages.Expired, messages.ReasonGenerationEnded); err != nil {
		return err
	}
	reply, err := s.report(s.working)
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadFenced {
		return Violatedf("a status after a cancel was answered %+v", reply.Rejected)
	}
	result, err := json.Marshal(laneResult(workorders.OutcomeFailed, workorders.ReasonFenced))
	if err != nil {
		return err
	}
	if err := accepted(s.upload(ledger.UploadLaneEnded, result)); err != nil {
		return err
	}
	if err := s.expectEnded(workorders.ReasonCanceled); err != nil {
		return err
	}
	if err := hasOrderState(r, s.store, s.grant.WorkOrder, workorders.Canceled); err != nil {
		return err
	}
	events, err := r.Events(s.store, r.Scope)
	if err != nil {
		return err
	}
	last := events[len(events)-1]
	if last.Type != ledger.EventLaneEnded || last.Subject.ID != s.lane {
		return Violatedf("a canceled lane's end recorded %+v", last)
	}
	// A retry of the same word is the same event, and a second word finds the lane over.
	again, err := s.upload(ledger.UploadLaneEnded, result)
	if err != nil {
		return err
	}
	if len(again.Rejected) != 1 {
		return Violatedf("a lane ended twice: %+v", again)
	}
	return r.CheckProjection(s.store)
}

func checkCancelStranger(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	stranger, err := newStatusRigOn(r, s.store, r.Ready)
	if err != nil {
		return err
	}
	if err := r.NewOps(s.store).Cancel(r.Ctx, r.Scope, s.grant.WorkOrder, "dropped", r.Actor); err != nil {
		return err
	}
	result, err := json.Marshal(laneResult(workorders.OutcomeFailed, "mine now"))
	if err != nil {
		return err
	}
	stranger.grant, stranger.lane = s.grant, s.lane
	reply, err := stranger.upload(ledger.UploadLaneEnded, result)
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 1 {
		return Violatedf("another worker ended a canceled lane: %+v", reply)
	}
	view, err := s.plane.Lane(r.Ctx, s.lane)
	if err != nil {
		return err
	}
	if view.ActualState == ledger.StateEnded {
		return Violatedf("a stranger's word ended lane %s", s.lane)
	}
	return nil
}

func checkCancelDesired(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	state, err := s.desired()
	if err != nil {
		return err
	}
	if len(state.Leases) != 1 {
		return Violatedf("the worker holds %+v", state.Leases)
	}
	if err := r.NewOps(s.store).Cancel(r.Ctx, r.Scope, s.grant.WorkOrder, "dropped", r.Actor); err != nil {
		return err
	}
	if state, err = s.desired(); err != nil {
		return err
	}
	if len(state.Leases) != 0 {
		return Violatedf("desired state still names %+v", state.Leases)
	}
	reply, err := s.plane.Heartbeat(r.Ctx, ledger.Heartbeat{
		Auth: s.auth, Epoch: s.epoch, Nonce: "n", Protocol: ledger.ProtocolVersion,
		Leases: []ledger.HeldLease{{WorkOrder: s.grant.WorkOrder, Claim: s.grant.ClaimID, Fence: s.grant.Fence}},
	})
	if err != nil {
		return err
	}
	if len(reply.Fenced) != 1 || len(reply.Leases) != 0 {
		return Violatedf("a beat after a cancel read %+v", reply)
	}
	return nil
}

func checkCancelRaces(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	for round := range 8 {
		id, err := r.Ready(s)
		if err != nil {
			return err
		}
		holder, err := r.Running(s, id, "a")
		if err != nil {
			return err
		}
		var group sync.WaitGroup
		var ended, canceled error
		group.Go(func() {
			if round%2 == 0 {
				ended = ops.EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeCompleted, ""), r.worker("a"))
				return
			}
			_, ended = ops.Write(r.Ctx, r.Scope, holder, r.ID("write"), []byte("racing"), r.worker("a"))
		})
		group.Go(func() { canceled = ops.Cancel(r.Ctx, r.Scope, id, "racing", r.Actor) })
		group.Wait()
		if canceled != nil {
			return canceled
		}
		var fenced *workorders.FencedError
		if ended != nil && !errors.As(ended, &fenced) {
			return Violatedf("the holder's step returned %v", ended)
		}
		if err := hasOrderState(r, s, id, workorders.Canceled); err != nil {
			return err
		}
	}
	return r.CheckProjection(s)
}

func checkCancelRecord(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	order, err := r.inState(s, workorders.Running)
	if err != nil {
		return err
	}
	if err := ops.Cancel(r.Ctx, r.Scope, order.ID, "", r.Actor); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a cancel with no reason returned %v", err)
	}
	if err := ops.Cancel(r.Ctx, r.Scope, order.ID, "the plan changed", r.Actor); err != nil {
		return err
	}
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	last := events[len(events)-1]
	if last.Type != workorders.EventType(workorders.TriggerCanceled) || last.Actor != r.Actor || !strings.Contains(string(last.Data), "the plan changed") {
		return Violatedf("a cancel recorded %+v", last)
	}
	return nil
}
