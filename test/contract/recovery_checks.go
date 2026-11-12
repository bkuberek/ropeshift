package contract

import (
	"encoding/json"
	"errors"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// RecoveryChecks are an orchestrator's recovery acts (FR-ORCH-8, ADR-0014): each acts within the recovery
// policy, records nothing on a subject another actor moved, and reaches a member as a request outside it.
var RecoveryChecks = []Check{
	{"RV1", "an orchestrator restarts a stalled lane on its machine, and the act names its generation, reason, and policy", checkRecoveryRestart},
	{"RV2", "a recovery act on work another actor moved records nothing", checkRecoveryMoved},
	{"RV3", "a lane on another machine reaches a member, once however often it is asked, and a grant restarts it as the member", checkRecoveryElsewhere},
	{"RV4", "an orchestrator retries failed work with attempts left, and a retry with none left reaches a member who adds one", checkRecoveryRetry},
	{"RV5", "an orchestrator resets a tripped breaker within the day's bound, and the next reset reaches a member", checkRecoveryResetBound},
	{"RV6", "a breaker its day tripped reaches a member", checkRecoveryDayCapped},
	{"RV7", "an orchestrator sends blocked work to planning with its lane's outcome, unless the policy turns that off", checkRecoveryReplan},
	{"RV8", "no orchestrator decides a recovery request, and one whose subject moved is void", checkRecoveryDecision},
	{"RV9", "a recovery act needs a reason, an orchestrator, and an act the design names", checkRecoveryRefusesTheRest},
	{"RV10", "the recovery policy is published like any other, and only by a member", checkRecoveryPolicyPublished},
	{"RV11", "work that names no bound of its own has the policy's attempts, and a retry past them reaches a member", checkRecoveryPolicyAttempts},
}

// recoveryRig is a running lane, with its worker's orchestrator started and in its third generation.
type recoveryRig struct {
	*statusRig
	call ledger.OrchestratorCall
}

const recoveryGeneration = 3

func newRecoveryRig(r *Rig) (*recoveryRig, error) {
	s, err := newStatusRig(r)
	if err != nil {
		return nil, err
	}
	return orchestrating(s)
}

// orchestrating starts a status rig's orchestrator, and has its worker report it in its third generation.
func orchestrating(s *statusRig) (*recoveryRig, error) {
	if _, err := s.plane.SetOrchestrator(s.r.Ctx, s.auth.WorkerID, ledger.OrchestratorRunning, theMember); err != nil {
		return nil, err
	}
	lane := ledger.Orchestrator{ID: ledger.OrchestratorOf(s.auth.WorkerID), Run: 1}.Lane()
	status, err := json.Marshal(ledger.LaneStatus{ActualState: "working", Liveness: "alive", ModelReach: "reachable", LedgerReach: "reachable"})
	if err != nil {
		return nil, err
	}
	s.seq++
	reply, err := s.plane.Upload(s.r.Ctx, ledger.UploadBatch{Auth: s.auth, Epoch: s.epoch, Events: []ledger.UploadEvent{{
		Epoch: s.epoch, Seq: s.seq, Type: ledger.UploadLaneStatus, Lane: lane, Generation: recoveryGeneration, Payload: status, OccurredAt: 1,
	}}})
	if err != nil || len(reply.Rejected) != 0 {
		return nil, Violatedf("the orchestrator's status was refused: %+v, %v", reply.Rejected, err)
	}
	return &recoveryRig{statusRig: s, call: ledger.OrchestratorCall{Auth: s.auth, Epoch: s.epoch, Lane: lane, WorkOrder: s.grant.WorkOrder}}, nil
}

// act is the orchestrator's call of one recovery tool.
func (o *recoveryRig) act(verb, reason string) (control.RecoveryResult, error) {
	call := o.call
	call.Reason = reason
	reply, err := o.plane.Orchestrate(o.r.Ctx, verb, call)
	if err != nil {
		return control.RecoveryResult{}, err
	}
	var result control.RecoveryResult
	err = json.Unmarshal(reply.Result, &result)
	return result, err
}

func (s *statusRig) laneStatus(state string, conditions ...string) error {
	status := s.working
	status.ActualState, status.Conditions = state, conditions
	reply, err := s.report(status)
	if err == nil && len(reply.Rejected) != 0 {
		return Violatedf("a status was refused: %+v", reply.Rejected)
	}
	return err
}

// recoveryRecords reads every recovery act on record, from the event log.
func recoveryRecords(s *statusRig) ([]authority.RecoveryRecord, error) {
	var out []authority.RecoveryRecord
	err := s.store.Read(s.r.Ctx, s.r.Scope, func(tx ports.ReadTx) error {
		for after := int64(0); ; {
			events, err := tx.Events(after, 500)
			if err != nil {
				return err
			}
			for _, event := range events {
				after = event.Rev
				if event.Type != authority.EventRecovery {
					continue
				}
				var record authority.RecoveryRecord
				if err := json.Unmarshal(event.Data, &record); err != nil {
					return err
				}
				out = append(out, record)
			}
			if len(events) < 500 {
				return nil
			}
		}
	})
	return out, err
}

// recoveryRequests reads a work order's recovery requests.
func recoveryRequests(s *statusRig, workOrder string) ([]authority.Approval, error) {
	var out []authority.Approval
	err := s.store.Read(s.r.Ctx, s.r.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Approvals().OfWorkOrder(workOrder)
		for _, row := range rows {
			if row.Kind == authority.KindRecovery {
				out = append(out, row)
			}
		}
		return err
	})
	return out, err
}

func (s *statusRig) laneCount(read func(ports.LaneReader) func(string) (int64, error)) (int64, error) {
	var n int64
	err := s.store.Read(s.r.Ctx, s.r.Scope, func(tx ports.ReadTx) error {
		var err error
		n, err = read(tx.Lanes())(s.lane)
		return err
	})
	return n, err
}

func respawnsOf(lanes ports.LaneReader) func(string) (int64, error)     { return lanes.Respawns }
func windowResetsOf(lanes ports.LaneReader) func(string) (int64, error) { return lanes.WindowResets }
func resetsOf(lanes ports.LaneReader) func(string) (int64, error)       { return lanes.Resets }

// grant is a member's approval of a request, as they read it.
func grant(s *statusRig, request authority.Approval) (authority.Approval, error) {
	return control.NewApprovals(s.store).Decide(s.r.Ctx, s.r.Scope, authority.Decision{ID: request.ID, Digest: request.SubjectDigest, Allow: true}, theMember)
}

func checkRecoveryRestart(r *Rig) error {
	o, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	if err := o.laneStatus("working", "stalled"); err != nil {
		return err
	}
	result, err := o.act(ledger.VerbRestart, "no progress for twenty minutes")
	if err != nil {
		return err
	}
	if result.Result != authority.RecoveryDone || result.Lane != o.lane || result.PolicyVersion != 1 {
		return Violatedf("the restart came to %+v", result)
	}
	if n, err := o.laneCount(respawnsOf); err != nil || n != 1 {
		return Violatedf("the lane was asked for %d generations: %v", n, err)
	}
	records, err := recoveryRecords(o.statusRig)
	if err != nil {
		return err
	}
	want := authority.RecoveryRecord{
		Act: authority.RecoverRestart, WorkOrder: o.grant.WorkOrder, Lane: o.lane, Actor: "orchestrator:" + o.auth.WorkerID,
		Generation: recoveryGeneration, Reason: "no progress for twenty minutes", PolicyVersion: 1, Result: authority.RecoveryDone,
	}
	if len(records) != 1 {
		return Violatedf("%d recovery acts on record", len(records))
	}
	got := records[0]
	got.ID, got.At = "", 0
	if got != want {
		return Violatedf("the act on record is %+v, want %+v", got, want)
	}
	return r.CheckProjection(o.store)
}

func checkRecoveryMoved(r *Rig) error {
	o, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	if err := o.laneStatus("working"); err != nil {
		return err
	}
	for _, verb := range []string{ledger.VerbRestart, ledger.VerbReset, ledger.VerbRetry, ledger.VerbReplan} {
		if _, err := o.act(verb, "it looks stuck"); !errors.Is(err, authority.ErrRecoveryMoved) {
			return Violatedf("%s on running work that is neither stalled, tripped, failed, nor blocked: %v", verb, err)
		}
	}
	records, err := recoveryRecords(o.statusRig)
	if err != nil {
		return err
	}
	requests, err := recoveryRequests(o.statusRig, o.grant.WorkOrder)
	if err != nil {
		return err
	}
	if len(records) != 0 || len(requests) != 0 {
		return Violatedf("a conflict left %d acts and %d requests", len(records), len(requests))
	}
	if n, err := o.laneCount(respawnsOf); err != nil || n != 0 {
		return Violatedf("a conflict asked for %d generations: %v", n, err)
	}
	return nil
}

func checkRecoveryElsewhere(r *Rig) error {
	o, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	if err := o.laneStatus("working", "stalled"); err != nil {
		return err
	}
	other, err := newStatusRigOn(r, o.store, r.Ready)
	if err != nil {
		return err
	}
	elsewhere, err := orchestrating(other)
	if err != nil {
		return err
	}
	elsewhere.call.WorkOrder = o.grant.WorkOrder
	first, err := elsewhere.act(ledger.VerbRestart, "it stalled")
	if err != nil {
		return err
	}
	if first.Result != authority.RecoveryRefused || first.Check != authority.CheckSameMachine || first.Approval == "" {
		return Violatedf("a restart of another machine's lane came to %+v", first)
	}
	again, err := elsewhere.act(ledger.VerbRestart, "it stalled")
	if err != nil {
		return err
	}
	if again.Approval != first.Approval {
		return Violatedf("a repeated call raised %s after %s", again.Approval, first.Approval)
	}
	requests, err := recoveryRequests(o.statusRig, o.grant.WorkOrder)
	if err != nil {
		return err
	}
	if len(requests) != 1 || !requests[0].Open() || requests[0].Lane != o.lane || requests[0].RequestedBy != "orchestrator:"+other.auth.WorkerID {
		return Violatedf("requests %+v", requests)
	}
	detail := authority.RecoveryDetailOf(requests[0])
	if detail.Check != authority.CheckSameMachine || detail.Reason != "it stalled" || detail.PolicyVersion != 1 || detail.Limit == "" {
		return Violatedf("the request reads %+v", detail)
	}
	if !strings.Contains(requests[0].Summary, "it stalled") || !strings.Contains(requests[0].Summary, detail.Limit) {
		return Violatedf("a member reads %q", requests[0].Summary)
	}
	if n, err := o.laneCount(respawnsOf); err != nil || n != 0 {
		return Violatedf("a refusal asked for %d generations: %v", n, err)
	}
	decided, err := grant(o.statusRig, requests[0])
	if err != nil {
		return err
	}
	if decided.State != authority.ApprovalApproved {
		return Violatedf("granted %+v", decided)
	}
	if n, err := o.laneCount(respawnsOf); err != nil || n != 1 {
		return Violatedf("a grant asked for %d generations: %v", n, err)
	}
	if requests, err = recoveryRequests(o.statusRig, o.grant.WorkOrder); err != nil {
		return err
	}
	if requests[0].State != authority.ApprovalUsed || requests[0].DecidedBy != authority.Principal(theMember) {
		return Violatedf("after the grant the request reads %+v", requests[0])
	}
	records, err := recoveryRecords(o.statusRig)
	if err != nil {
		return err
	}
	if len(records) != 3 || records[0].Result != authority.RecoveryRefused || records[1].Result != authority.RecoveryRefused ||
		records[2].Result != authority.RecoveryDone || records[2].Actor != authority.Principal(theMember) || records[2].Approval != first.Approval {
		return Violatedf("acts on record %+v", records)
	}
	if records[0].Generation != recoveryGeneration || records[0].Approval != first.Approval || records[0].Check != authority.CheckSameMachine {
		return Violatedf("the refusal on record %+v", records[0])
	}
	return nil
}

// failedRecoveryRig is an orchestrated lane whose work failed, with the attempts a work order allows.
func failedRecoveryRig(r *Rig, attempts int64) (*recoveryRig, error) {
	return failedRecoveryRigOn(r, r.Store(), attempts)
}

// failedRecoveryRigOn is failedRecoveryRig in a store a test chose.
func failedRecoveryRigOn(r *Rig, store ports.Store, attempts int64) (*recoveryRig, error) {
	r.MaxAttempts = attempts
	defer func() { r.MaxAttempts = 0 }()
	s, err := newStatusRigOn(r, store, r.Ready)
	if err != nil {
		return nil, err
	}
	o, err := orchestrating(s)
	if err != nil {
		return nil, err
	}
	result, err := json.Marshal(workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeFailed, Reason: workorders.ReasonCrashed}})
	if err != nil {
		return nil, err
	}
	if reply, err := o.upload(ledger.UploadLaneEnded, result); err != nil || len(reply.Rejected) != 0 {
		return nil, Violatedf("the lane did not end: %+v, %v", reply.Rejected, err)
	}
	return o, nil
}

func checkRecoveryRetry(r *Rig) error {
	o, err := failedRecoveryRig(r, 3)
	if err != nil {
		return err
	}
	result, err := o.act(ledger.VerbRetry, "the network came back")
	if err != nil {
		return err
	}
	order, err := o.order()
	if err != nil {
		return err
	}
	if result.Result != authority.RecoveryDone || result.Lane != o.lane || order.State != workorders.Ready || order.MaxAttempts != 3 {
		return Violatedf("a retry with attempts left came to %+v, and the work reads %s with %d of %d", result, order.State, order.Attempts, order.MaxAttempts)
	}
	if err := retryNoted(o.store, r, order.ID, workorders.ReasonCrashed, "the network came back"); err != nil {
		return err
	}
	spent, err := failedRecoveryRig(r, 1)
	if err != nil {
		return err
	}
	refused, err := spent.act(ledger.VerbRetry, "try once more")
	if err != nil {
		return err
	}
	if refused.Result != authority.RecoveryRefused || refused.Check != authority.CheckAttempts {
		return Violatedf("a retry with no attempts left came to %+v", refused)
	}
	if order, err = spent.order(); err != nil {
		return err
	}
	if order.State != workorders.Failed {
		return Violatedf("a refused retry moved the work to %s", order.State)
	}
	requests, err := recoveryRequests(spent.statusRig, order.ID)
	if err != nil {
		return err
	}
	if len(requests) != 1 || requests[0].Lane != "" || requests[0].Subject != string(authority.RecoverRetry) {
		return Violatedf("requests %+v", requests)
	}
	if _, err := grant(spent.statusRig, requests[0]); err != nil {
		return err
	}
	if order, err = spent.order(); err != nil {
		return err
	}
	if order.State != workorders.Ready || order.Attempts != 1 || order.MaxAttempts != 2 {
		return Violatedf("a member's grant left %s with %d of %d attempts", order.State, order.Attempts, order.MaxAttempts)
	}
	return r.CheckProjection(o.store)
}

func checkRecoveryResetBound(r *Rig) error {
	o, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	for i := range 2 {
		if err := o.laneStatus("tripped"); err != nil {
			return err
		}
		result, err := o.act(ledger.VerbReset, "a flaky test crashed it")
		if err != nil {
			return err
		}
		if result.Result != authority.RecoveryDone {
			return Violatedf("reset %d of the day's two came to %+v", i+1, result)
		}
		// The worker restarts the lane, and its next crash trips the breaker again.
		if err := o.laneStatus("working"); err != nil {
			return err
		}
	}
	if n, err := o.laneCount(windowResetsOf); err != nil || n != 2 {
		return Violatedf("%d window resets: %v", n, err)
	}
	if n, err := o.laneCount(resetsOf); err != nil || n != 0 {
		return Violatedf("an orchestrator's reset counted as a member's: %d, %v", n, err)
	}
	if err := o.laneStatus("tripped"); err != nil {
		return err
	}
	third, err := o.act(ledger.VerbReset, "a flaky test crashed it")
	if err != nil {
		return err
	}
	if third.Result != authority.RecoveryRefused || third.Check != authority.CheckBreakerResets {
		return Violatedf("a reset past the day's bound came to %+v", third)
	}
	requests, err := recoveryRequests(o.statusRig, o.grant.WorkOrder)
	if err != nil {
		return err
	}
	if len(requests) != 1 {
		return Violatedf("requests %+v", requests)
	}
	if _, err := grant(o.statusRig, requests[0]); err != nil {
		return err
	}
	if n, err := o.laneCount(resetsOf); err != nil || n != 1 {
		return Violatedf("a member's grant made %d resets that override the day: %v", n, err)
	}
	return nil
}

func checkRecoveryDayCapped(r *Rig) error {
	o, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	if err := o.laneStatus("tripped", "day_capped"); err != nil {
		return err
	}
	result, err := o.act(ledger.VerbReset, "it ran out of generations")
	if err != nil {
		return err
	}
	if result.Result != authority.RecoveryRefused || result.Check != authority.CheckDayCap {
		return Violatedf("a reset of a lane its day tripped came to %+v", result)
	}
	if n, err := o.laneCount(windowResetsOf); err != nil || n != 0 {
		return Violatedf("a refused reset counted: %d, %v", n, err)
	}
	return nil
}

func checkRecoveryReplan(r *Rig) error {
	s, err := blockedWork(r)
	if err != nil {
		return err
	}
	o, err := orchestrating(s)
	if err != nil {
		return err
	}
	result, err := o.act(ledger.VerbReplan, "the contract names a file that does not exist")
	if err != nil {
		return err
	}
	order, err := o.order()
	if err != nil {
		return err
	}
	if result.Result != authority.RecoveryDone || order.State != workorders.Planning || order.ReviewedBy != "" {
		return Violatedf("a replan came to %+v, and the work reads %+v", result, order)
	}
	var replanned workorders.Change
	err = o.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: order.ID})
		if err != nil {
			return err
		}
		return json.Unmarshal(events[len(events)-1].Data, &replanned)
	})
	if err != nil {
		return err
	}
	if replanned.Trigger != workorders.TriggerReplan || replanned.Outcome != workorders.OutcomeBlocked || replanned.Lane != o.lane ||
		!strings.Contains(string(replanned.Payload), "no_outcome") || replanned.Reason != "the contract names a file that does not exist" {
		return Violatedf("the replan on record %+v", replanned)
	}
	off, err := blockedWork(r)
	if err != nil {
		return err
	}
	body := strings.Replace(strings.Replace(authority.BuiltInRecoveryPolicyBody, "version = 1", "version = 2", 1), "replan_blocked = true", "replan_blocked = false", 1)
	policy := authority.DefaultRecoveryPolicy()
	policy.Version, policy.ReplanBlocked = 2, false
	if _, err := control.NewRecoveries(off.store).PublishRecoveryPolicy(r.Ctx, r.Scope, authority.RecoveryPolicyDraft{Body: body, Policy: policy}, theMember); err != nil {
		return err
	}
	held, err := orchestrating(off)
	if err != nil {
		return err
	}
	refused, err := held.act(ledger.VerbReplan, "the contract is wrong")
	if err != nil {
		return err
	}
	if refused.Result != authority.RecoveryRefused || refused.Check != authority.CheckReplanBlocked || refused.PolicyVersion != 2 {
		return Violatedf("a replan the policy turned off came to %+v", refused)
	}
	return r.CheckProjection(o.store)
}

func checkRecoveryDecision(r *Rig) error {
	o, err := failedRecoveryRig(r, 1)
	if err != nil {
		return err
	}
	if _, err := o.act(ledger.VerbRetry, "try once more"); err != nil {
		return err
	}
	requests, err := recoveryRequests(o.statusRig, o.grant.WorkOrder)
	if err != nil || len(requests) != 1 {
		return Violatedf("requests %+v: %v", requests, err)
	}
	for _, actor := range []ledger.Actor{{Kind: ledger.ActorOrchestrator, ID: o.auth.WorkerID}, {Kind: ledger.ActorOrchestrator, ID: "another"}} {
		decision := authority.Decision{ID: requests[0].ID, Digest: requests[0].SubjectDigest, Allow: true}
		if _, err := control.NewApprovals(o.store).Decide(r.Ctx, r.Scope, decision, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("orchestrator %s decided a recovery request: %v", actor.ID, err)
		}
	}
	if _, err := control.NewRecoveries(o.store).Retry(r.Ctx, r.Scope, o.grant.WorkOrder, "a member took it", theMember); err != nil {
		return err
	}
	if requests, err = recoveryRequests(o.statusRig, o.grant.WorkOrder); err != nil {
		return err
	}
	if requests[0].State != authority.ApprovalVoid {
		return Violatedf("a request whose work a member retried reads %s", requests[0].State)
	}
	// A lane its day tripped asks a member for a reset, and starts again on a member's word before the
	// request is decided: the request is bound to a tripped lane, so a grant finds it void.
	again, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	if err := again.laneStatus("tripped", "day_capped"); err != nil {
		return err
	}
	if _, err := again.act(ledger.VerbReset, "it ran out of generations"); err != nil {
		return err
	}
	stale, err := recoveryRequests(again.statusRig, again.grant.WorkOrder)
	if err != nil || len(stale) != 1 {
		return Violatedf("requests %+v: %v", stale, err)
	}
	if err := again.laneStatus("working"); err != nil {
		return err
	}
	if _, err := grant(again.statusRig, stale[0]); !errors.Is(err, authority.ErrApprovalClosed) {
		return Violatedf("a grant whose lane moved: %v", err)
	}
	if stale, err = recoveryRequests(again.statusRig, again.grant.WorkOrder); err != nil {
		return err
	}
	if stale[0].State != authority.ApprovalVoid {
		return Violatedf("a request whose lane moved reads %s", stale[0].State)
	}
	if n, err := again.laneCount(resetsOf); err != nil || n != 0 {
		return Violatedf("a void request reset the lane %d times: %v", n, err)
	}
	return nil
}

func checkRecoveryRefusesTheRest(r *Rig) error {
	o, err := failedRecoveryRig(r, 3)
	if err != nil {
		return err
	}
	if _, err := o.act(ledger.VerbRetry, "  "); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a retry with no reason: %v", err)
	}
	recoveries := control.NewRecoveries(o.store)
	call := control.RecoveryCall{Act: authority.RecoverRetry, WorkOrder: o.grant.WorkOrder, Reason: "it might work"}
	for _, actor := range []ledger.Actor{{Kind: ledger.ActorLane, ID: o.lane}, {Kind: ledger.ActorWorker, ID: o.auth.WorkerID}, {Kind: ledger.ActorSystem, ID: "tick"}} {
		if _, err := recoveries.Recover(r.Ctx, r.Scope, call, actor, 0); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s took a recovery act: %v", actor.Kind, err)
		}
	}
	takeover := call
	takeover.Act = "work_takeover"
	if _, err := recoveries.Recover(r.Ctx, r.Scope, takeover, ledger.Actor{Kind: ledger.ActorOrchestrator, ID: o.auth.WorkerID}, 0); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a takeover passed as a recovery act: %v", err)
	}
	records, err := recoveryRecords(o.statusRig)
	if err != nil || len(records) != 0 {
		return Violatedf("refused calls left %+v: %v", records, err)
	}
	order, err := o.order()
	if err != nil || order.State != workorders.Failed {
		return Violatedf("refused calls moved the work: %+v, %v", order, err)
	}
	return nil
}

func checkRecoveryPolicyPublished(r *Rig) error {
	recoveries := control.NewRecoveries(r.Store())
	versions, err := recoveries.RecoveryPolicyVersions(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(versions) != 1 || versions[0].Kind != authority.PolicyRecovery || versions[0].Body != authority.BuiltInRecoveryPolicyBody {
		return Violatedf("a workspace starts on %+v", versions)
	}
	policy := authority.DefaultRecoveryPolicy()
	policy.Version, policy.MaxBreakerResetsPerDay = 2, 5
	draft := authority.RecoveryPolicyDraft{Body: "[recovery_policy]\nversion = 2\n", Policy: policy}
	if _, err := recoveries.PublishRecoveryPolicy(r.Ctx, r.Scope, draft, ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "o"}); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("an orchestrator moved its own bound: %v", err)
	}
	if _, err := recoveries.PublishRecoveryPolicy(r.Ctx, r.Scope, draft, theMember); err != nil {
		return err
	}
	if _, err := recoveries.PublishRecoveryPolicy(r.Ctx, r.Scope, draft, theMember); err != nil {
		return Violatedf("a retry failed: %v", err)
	}
	stale := authority.RecoveryPolicyDraft{Body: "[recovery_policy]\nversion = 2\n# again\n", Policy: policy}
	if _, err := recoveries.PublishRecoveryPolicy(r.Ctx, r.Scope, stale, theMember); !errors.Is(err, authority.ErrBadRecoveryPolicy) {
		return Violatedf("a version that is not newer read %v", err)
	}
	versions, err = recoveries.RecoveryPolicyVersions(r.Ctx, r.Scope)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 {
		return Violatedf("versions %+v: %v", versions, err)
	}
	return nil
}

// lastTrigger reads the trigger of a work order's last move.
func lastTrigger(s *statusRig, id string) (workorders.Trigger, error) {
	var change workorders.Change
	err := s.store.Read(s.r.Ctx, s.r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: id})
		if err != nil {
			return err
		}
		return json.Unmarshal(events[len(events)-1].Data, &change)
	})
	return change.Trigger, err
}

func checkRecoveryPolicyAttempts(r *Rig) error {
	within, err := failedRecoveryRig(r, -1)
	if err != nil {
		return err
	}
	if trigger, err := lastTrigger(within.statusRig, within.grant.WorkOrder); err != nil || trigger != workorders.TriggerLaneFailed {
		return Violatedf("a first failure under the policy's three attempts ended as %s: %v", trigger, err)
	}
	result, err := within.act(ledger.VerbRetry, "the network came back")
	if err != nil {
		return err
	}
	order, err := within.order()
	if err != nil {
		return err
	}
	if result.Result != authority.RecoveryDone || order.State != workorders.Ready || order.MaxAttempts != 0 {
		return Violatedf("a retry within the policy's attempts came to %+v, and the work reads %s with a bound of %d", result, order.State, order.MaxAttempts)
	}
	body := strings.Replace(strings.Replace(authority.BuiltInRecoveryPolicyBody, "version = 1", "version = 2", 1), "max_attempts = 3", "max_attempts = 1", 1)
	policy := authority.DefaultRecoveryPolicy()
	policy.Version, policy.MaxAttempts = 2, 1
	if _, err := control.NewRecoveries(within.store).PublishRecoveryPolicy(r.Ctx, r.Scope, authority.RecoveryPolicyDraft{Body: body, Policy: policy}, theMember); err != nil {
		return err
	}
	spent, err := failedRecoveryRigOn(r, within.store, -1)
	if err != nil {
		return err
	}
	if trigger, err := lastTrigger(spent.statusRig, spent.grant.WorkOrder); err != nil || trigger != workorders.TriggerAttemptsExhausted {
		return Violatedf("a failure under a policy of one attempt ended as %s: %v", trigger, err)
	}
	refused, err := spent.act(ledger.VerbRetry, "try once more")
	if err != nil {
		return err
	}
	if refused.Result != authority.RecoveryRefused || refused.Check != authority.CheckAttempts || refused.PolicyVersion != 2 || !strings.Contains(refused.Limit, "1 of max_attempts 1") {
		return Violatedf("a retry past the policy's attempts came to %+v", refused)
	}
	requests, err := recoveryRequests(spent.statusRig, spent.grant.WorkOrder)
	if err != nil || len(requests) != 1 {
		return Violatedf("requests %+v: %v", requests, err)
	}
	if _, err := grant(spent.statusRig, requests[0]); err != nil {
		return err
	}
	if order, err = spent.order(); err != nil {
		return err
	}
	if order.State != workorders.Ready || order.Attempts != 1 || order.MaxAttempts != 2 {
		return Violatedf("a member's grant left %s with %d of %d attempts", order.State, order.Attempts, order.MaxAttempts)
	}
	return r.CheckProjection(within.store)
}
