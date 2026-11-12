package control

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Recoveries holds the use cases that recover stuck work (FR-ORCH-8): the versioned policy that bounds an
// orchestrator, and the acts it and a member take.
type Recoveries struct {
	store ports.Store
}

// NewRecoveries returns the use cases over a Store.
func NewRecoveries(store ports.Store) *Recoveries { return &Recoveries{store: store} }

// currentRecoveryVersion reads the version of the recovery policy in force, as currentPolicy does for
// decisions, so every recovery act names a version a member can read.
func currentRecoveryVersion(tx ports.WriteTx) (authority.PolicyVersion, error) {
	version, err := tx.Policies().InForce(authority.PolicyRecovery)
	if !errors.Is(err, ports.ErrNotFound) {
		return version, err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	if version, err = builtInRecoveryPolicy(id, tx.Now()); err != nil {
		return authority.PolicyVersion{}, err
	}
	return version, insertPolicy(tx, version, policyActor)
}

func builtInRecoveryPolicy(id string, createdAt int64) (authority.PolicyVersion, error) {
	draft := authority.RecoveryPolicyDraft{Body: authority.BuiltInRecoveryPolicyBody, Policy: authority.DefaultRecoveryPolicy()}
	return authority.NewRecoveryPolicyVersion(id, builtInPolicyAuthor, draft, createdAt)
}

// PublishRecoveryPolicy records a new version of the recovery policy, on the same terms as every other
// policy: only a member can, each version is newer than the last, and the same body is a retry. No
// orchestrator can move the bound it is held to.
func (r *Recoveries) PublishRecoveryPolicy(ctx context.Context, scope ledger.WorkspaceID, draft authority.RecoveryPolicyDraft, actor ledger.Actor) (authority.PolicyVersion, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return authority.PolicyVersion{}, workorders.ErrForbidden
	}
	var record authority.PolicyVersion
	err := r.store.Write(ctx, scope, "policy.publish", func(tx ports.WriteTx) error {
		current, err := currentRecoveryVersion(tx)
		if err != nil {
			return err
		}
		if current.Version == draft.Policy.Version && current.Digest == authority.PolicyDigest(draft.Body) {
			record = current
			return nil
		}
		if err := draft.Policy.Check(); err != nil {
			return err
		}
		if draft.Policy.Version <= current.Version {
			return fmt.Errorf("%w: version %d is not newer than version %d", authority.ErrBadRecoveryPolicy, draft.Policy.Version, current.Version)
		}
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		if record, err = authority.NewRecoveryPolicyVersion(id, actor.ID, draft, tx.Now()); err != nil {
			return err
		}
		return insertPolicy(tx, record, actor)
	})
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return record, nil
}

// RecoveryPolicyVersions lists the versions of the recovery policy, newest first. A workspace nothing has
// recovered in yet still has the built-in one in force.
func (r *Recoveries) RecoveryPolicyVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error) {
	var versions []authority.PolicyVersion
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		versions, err = tx.Policies().Versions(authority.PolicyRecovery)
		return err
	})
	if err != nil || len(versions) != 0 {
		return versions, err
	}
	version, err := builtInRecoveryPolicy(builtInPolicyAuthor, 0)
	if err != nil {
		return nil, err
	}
	return []authority.PolicyVersion{version}, nil
}

// laneEnds are the triggers of a lane's end, which carry its outcome and the reason it gave.
var laneEnds = []workorders.Trigger{
	workorders.TriggerLaneCompleted, workorders.TriggerLaneBlocked, workorders.TriggerLaneFailed, workorders.TriggerLaneFailedRetry,
	workorders.TriggerAttemptsExhausted,
}

// lastLaneEnd reads how a work order's last lane ended: the lane, its outcome, the reason it gave, and
// what it reported. A work order no lane ended reads as nothing.
func lastLaneEnd(tx ports.WriteTx, workOrder string) (workorders.Change, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: workOrder})
	if err != nil {
		return workorders.Change{}, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		var change workorders.Change
		if json.Unmarshal(events[i].Data, &change) != nil {
			continue
		}
		for _, end := range laneEnds {
			if change.Trigger == end {
				return change, nil
			}
		}
	}
	return workorders.Change{}, nil
}

// retryWork returns failed work to `ready` for another attempt, and keeps why with the work order, so the
// next lane's brief carries the failed lane's reason and the retry's (ADR-0014). Work with no attempts
// left gets one more, which only a member's retry or a member's grant reaches: its bound becomes its own,
// one past the attempts it used. Work with attempts left keeps whatever bound it had, its own or none. Work that was ready when its
// lane started is ready again, as it is after a takeover, so the definition of ready does not run.
func retryWork(tx ports.WriteTx, current workorders.WorkOrder, reason string, actor ledger.Actor) (workorders.State, error) {
	if _, err := workorders.Move(current, workorders.TriggerRetried); err != nil {
		return "", err
	}
	change := workorders.Change{Trigger: workorders.TriggerRetried, Reason: reason}
	byDefault, err := defaultAttempts(tx.Policies())
	if err != nil {
		return "", err
	}
	bound := current.MaxAttempts
	if !current.AttemptsLeft(byDefault) {
		bound = max(current.AttemptBound(byDefault), current.Attempts) + 1
		change.MaxAttempts = bound
	}
	won, err := tx.WorkOrders().Retry(current.ID, bound)
	if err != nil {
		return "", err
	}
	if !won {
		return "", fmt.Errorf("%w: work order %s is no longer failed", workorders.ErrIllegalMove, current.ID)
	}
	if err := voidRecoveries(tx, current.ID); err != nil {
		return "", err
	}
	ended, err := lastLaneEnd(tx, current.ID)
	if err != nil {
		return "", err
	}
	change.Lane = ended.Lane
	event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerRetried), current.ID, change, actor)
	if err != nil {
		return "", err
	}
	if _, err := tx.Append(event); err != nil {
		return "", err
	}
	why := cmp.Or(ended.Reason, string(ended.Outcome), string(workorders.OutcomeFailed))
	note := workorders.ReworkNote{
		WorkOrder: current.ID, Attempt: current.Fence,
		Reason: fmt.Sprintf("The last attempt's lane failed (%s), and the work was retried: %s", why, reason),
	}
	if err := tx.Evidence().RecordRework(note); err != nil {
		return "", err
	}
	return workorders.Ready, nil
}

// Retry is a member's retry of failed work, with why. A member is bound by no recovery policy, so work
// with no attempts left gets one more.
func (r *Recoveries) Retry(ctx context.Context, scope ledger.WorkspaceID, id, reason string, actor ledger.Actor) (workorders.State, error) {
	if !authority.Allowed(actor, authority.ActionRetryWork) {
		return "", workorders.ErrForbidden
	}
	if strings.TrimSpace(reason) == "" {
		return "", fmt.Errorf("%w: a retry needs a reason", workorders.ErrInvalid)
	}
	var state workorders.State
	err := r.store.Write(ctx, scope, "work_order.retry", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		state, err = retryWork(tx, current, reason, actor)
		return err
	})
	if err != nil {
		return "", err
	}
	return state, nil
}

// policyReader reads the version of a policy in force.
type policyReader interface {
	InForce(kind authority.PolicyKind) (authority.PolicyVersion, error)
}

// defaultAttempts is the attempts the recovery policy in force gives a work order that names no bound. A
// workspace that recorded no version yet runs on the built-in one, and nothing here writes it.
func defaultAttempts(policies policyReader) (int64, error) {
	version, err := policies.InForce(authority.PolicyRecovery)
	if errors.Is(err, ports.ErrNotFound) {
		return authority.DefaultRecoveryPolicy().MaxAttempts, nil
	}
	if err != nil {
		return 0, err
	}
	policy, err := authority.ReadRecoveryPolicy(version)
	if err != nil {
		return 0, err
	}
	return policy.AttemptBound(0), nil
}

// recoveryPolicyInForce reads what the version in force says. A body that no longer matches its digest
// stops the act, because a member reviewed other text.
func recoveryPolicyInForce(tx ports.WriteTx) (authority.RecoveryPolicy, authority.PolicyVersion, error) {
	version, err := currentRecoveryVersion(tx)
	if err != nil {
		return authority.RecoveryPolicy{}, authority.PolicyVersion{}, err
	}
	policy, err := authority.ReadRecoveryPolicy(version)
	return policy, version, err
}

// RecoveryCall is one recovery act as its caller asked for it.
type RecoveryCall struct {
	Act       authority.RecoveryAct
	WorkOrder string
	Reason    string
}

// RecoveryResult is what a call came to: done, or refused with the check that failed, the limit it went
// past, and the request that now waits for a member.
type RecoveryResult struct {
	Act           authority.RecoveryAct `json:"act"`
	WorkOrder     string                `json:"work_order"`
	Lane          string                `json:"lane,omitempty"`
	Result        string                `json:"result"`
	Check         string                `json:"check,omitempty"`
	Limit         string                `json:"limit,omitempty"`
	Approval      string                `json:"approval,omitempty"`
	PolicyVersion int64                 `json:"policy_version,omitempty"`
}

// recoverySubject is what a recovery act starts from: the work order, and for a lane act the claim and the
// lane it runs. ended is how the work order's last lane ended, which a retry and a replan carry on.
type recoverySubject struct {
	order workorders.WorkOrder
	claim workorders.Claim
	lane  ledger.LaneRow
	ended workorders.Change
}

func movedf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{authority.ErrRecoveryMoved}, args...)...)
}

// readRecoverySubject reads the subject of an act, and refuses one that is no longer in the state the act
// starts from: another actor moved it.
func readRecoverySubject(tx ports.WriteTx, act authority.RecoveryAct, id string) (recoverySubject, error) {
	order, err := tx.WorkOrders().Get(id)
	if err != nil {
		return recoverySubject{}, err
	}
	subject := recoverySubject{order: order}
	switch act {
	case authority.RecoverRestart, authority.RecoverReset:
		if order.State != workorders.Running {
			return subject, movedf("work order %s is %s, so it runs no lane", id, order.State)
		}
		if subject.claim, err = tx.Claims().Find(order.ClaimID); err != nil {
			return subject, err
		}
		if subject.claim.Lane == "" {
			return subject, movedf("work order %s runs no lane yet", id)
		}
		subject.lane, err = tx.Lanes().Row(subject.claim.Lane)
		if errors.Is(err, ports.ErrNotFound) {
			return subject, movedf("lane %s has reported nothing yet", subject.claim.Lane)
		}
		if err != nil {
			return subject, err
		}
		state := lanes.ActualState(subject.lane.Status.ActualState)
		stalled := (state == lanes.Working || state == lanes.Compacting) && slices.Contains(subject.lane.Status.Conditions, string(lanes.Stalled))
		if act == authority.RecoverRestart && !stalled {
			return subject, movedf("lane %s is %s and not stalled", subject.lane.ID, state)
		}
		if act == authority.RecoverReset && state != lanes.Tripped {
			return subject, movedf("lane %s is %s, not tripped", subject.lane.ID, state)
		}
	case authority.RecoverRetry, authority.RecoverReplan:
		from := workorders.Failed
		if act == authority.RecoverReplan {
			from = workorders.Blocked
		}
		if order.State != from {
			return subject, movedf("work order %s is %s, not %s", id, order.State, from)
		}
		if subject.ended, err = lastLaneEnd(tx, id); err != nil {
			return subject, err
		}
	default:
		return subject, fmt.Errorf("%w: %q is no recovery act", workorders.ErrInvalid, act)
	}
	return subject, nil
}

// laneOf names the lane an act is about: the one it works on, or the one whose end left the work where
// it is.
func (s recoverySubject) laneOf(act authority.RecoveryAct) string {
	if act.LaneAct() {
		return s.lane.ID
	}
	return s.ended.Lane
}

// digest covers what any move of the subject changes, so a request bound to it is void once it moves.
func (s recoverySubject) digest(act authority.RecoveryAct) string {
	number := func(n int64) string { return strconv.FormatInt(n, 10) }
	parts := []string{
		s.order.ID, string(s.order.State), number(s.order.Fence), number(s.order.Rev), number(s.order.Attempts), number(s.order.MaxAttempts),
	}
	if act.LaneAct() {
		parts = append(parts, s.lane.ID, number(s.lane.Generation), s.lane.Status.ActualState)
	}
	return authority.RecoveryDigest(act, parts...)
}

// recoveryFacts is what the policy reads of a subject, for the orchestrator that asks.
func recoveryFacts(tx ports.WriteTx, act authority.RecoveryAct, s recoverySubject, actor ledger.Actor) (authority.RecoveryFacts, error) {
	facts := authority.RecoveryFacts{Act: act, Attempts: s.order.Attempts, MaxAttempts: s.order.MaxAttempts}
	if !act.LaneAct() {
		return facts, nil
	}
	facts.OnMachine = s.lane.WorkerID == actor.ID
	facts.DayCapped = slices.Contains(s.lane.Status.Conditions, string(lanes.DayCapped))
	// The day is a sliding one, as the breaker's cap on generations reads it.
	var err error
	facts.ResetsToday, err = tx.Recoveries().Done(s.order.ID, authority.RecoverReset, ledger.ActorOrchestrator, tx.Now()-dayMs)
	return facts, err
}

// Recover takes one recovery act (FR-ORCH-8). An orchestrator is held to the recovery policy in force: an
// act within it runs, and one outside it raises a request only a member decides. A member is held to none.
// A subject another actor already moved records nothing. Every act that runs or is refused is a `recovery`
// event, with the orchestrator's generation, the reason, and the policy version, and no act is a takeover.
func (r *Recoveries) Recover(ctx context.Context, scope ledger.WorkspaceID, call RecoveryCall, actor ledger.Actor, generation int64) (RecoveryResult, error) {
	if !authority.Allowed(actor, authority.ActionRecover) {
		return RecoveryResult{}, workorders.ErrForbidden
	}
	if !authority.KnownRecoveryAct(call.Act) {
		return RecoveryResult{}, fmt.Errorf("%w: %q is no recovery act", workorders.ErrInvalid, call.Act)
	}
	if call.WorkOrder == "" {
		return RecoveryResult{}, fmt.Errorf("%w: a recovery act names its work order", workorders.ErrInvalid)
	}
	if strings.TrimSpace(call.Reason) == "" {
		return RecoveryResult{}, fmt.Errorf("%w: a recovery act needs a reason", workorders.ErrInvalid)
	}
	var result RecoveryResult
	err := r.store.Write(ctx, scope, "recovery.act", func(tx ports.WriteTx) error {
		subject, err := readRecoverySubject(tx, call.Act, call.WorkOrder)
		if err != nil {
			return err
		}
		policy, version, err := recoveryPolicyInForce(tx)
		if err != nil {
			return err
		}
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		lane := subject.laneOf(call.Act)
		result = RecoveryResult{Act: call.Act, WorkOrder: call.WorkOrder, Lane: lane, Result: authority.RecoveryDone}
		record := authority.RecoveryRecord{
			ID: id, Act: call.Act, WorkOrder: call.WorkOrder, Lane: lane, Actor: authority.Principal(actor), Generation: generation,
			Reason: call.Reason, Result: authority.RecoveryDone, At: tx.Now(),
		}
		if !authority.Allowed(actor, authority.ActionPassRecovery) {
			result.PolicyVersion, record.PolicyVersion = version.Version, version.Version
			facts, err := recoveryFacts(tx, call.Act, subject, actor)
			if err != nil {
				return err
			}
			if refusal, refused := policy.Judge(facts); refused {
				request, err := raiseRecovery(tx, call, subject, refusal, version.Version, actor)
				if err != nil {
					return err
				}
				result.Result, result.Check, result.Limit, result.Approval = authority.RecoveryRefused, refusal.Check, refusal.Limit, request.ID
				record.Result, record.Check, record.Limit, record.Approval = authority.RecoveryRefused, refusal.Check, refusal.Limit, request.ID
				return keepRecovery(tx, record, actor)
			}
		}
		if err := performRecovery(tx, call.Act, subject, call.Reason, actor); err != nil {
			return err
		}
		return keepRecovery(tx, record, actor)
	})
	if err != nil {
		return RecoveryResult{}, err
	}
	return result, nil
}

// performRecovery runs an act on its subject. A lane act asks the worker that holds the lane, so the lane's
// state still derives from what happens next: a restart is the lane's next generation, with start reason
// `user_respawn`, and a reset closes its breaker.
func performRecovery(tx ports.WriteTx, act authority.RecoveryAct, s recoverySubject, reason string, actor ledger.Actor) error {
	switch act {
	case authority.RecoverRestart:
		return respawnLane(tx, s.claim, actor)
	case authority.RecoverReset:
		return resetLane(tx, s.order.ID, s.lane.ID, actor)
	case authority.RecoverRetry:
		_, err := retryWork(tx, s.order, reason, actor)
		return err
	case authority.RecoverReplan:
		change := workorders.Change{
			Trigger: workorders.TriggerReplan, Reason: reason, Lane: s.ended.Lane, Outcome: s.ended.Outcome, Payload: s.ended.Payload,
		}
		return unblock(tx, s.order, change, actor)
	}
	return fmt.Errorf("%w: %q is no recovery act", workorders.ErrInvalid, act)
}

// raiseRecovery records the request a refused act leaves for a member. One that stands open for this act
// and this subject is that request, so a repeated call raises nothing new.
func raiseRecovery(tx ports.WriteTx, call RecoveryCall, s recoverySubject, refusal authority.Refusal, policy int64, actor ledger.Actor) (authority.Approval, error) {
	digest := s.digest(call.Act)
	rows, err := tx.Approvals().OfWorkOrder(call.WorkOrder)
	if err != nil {
		return authority.Approval{}, err
	}
	for _, row := range rows {
		if row.Kind == authority.KindRecovery && row.Open() && row.Subject == string(call.Act) && row.SubjectDigest == digest {
			return row, nil
		}
	}
	raw, err := json.Marshal(map[string]string{"work_order": call.WorkOrder, "reason": call.Reason})
	if err != nil {
		return authority.Approval{}, err
	}
	input, err := authority.CanonicalInput(raw)
	if err != nil {
		return authority.Approval{}, err
	}
	lane := s.laneOf(call.Act)
	detail, err := json.Marshal(authority.RecoveryDetail{
		Act: call.Act, WorkOrder: call.WorkOrder, Lane: lane, Reason: call.Reason, Check: refusal.Check, Limit: refusal.Limit, PolicyVersion: policy,
	})
	if err != nil {
		return authority.Approval{}, err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return authority.Approval{}, err
	}
	request := authority.Approval{
		ID: id, WorkOrder: call.WorkOrder, Kind: authority.KindRecovery, Subject: string(call.Act), SubjectDigest: digest, Input: input,
		Summary: fmt.Sprintf("%s on work order %s, which the orchestrator of %s asked for because: %s. It is outside version %d of the recovery policy: %s",
			call.Act, call.WorkOrder, actor.ID, call.Reason, policy, refusal.Limit),
		RequestedBy: authority.Principal(actor), State: authority.ApprovalPending, RaisedAt: tx.Now(), Detail: string(detail),
	}
	if call.Act.LaneAct() {
		request.Lane, request.Generation = lane, s.lane.Generation
	}
	if err := request.Validate(); err != nil {
		return authority.Approval{}, fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	added, err := tx.Approvals().Insert(request)
	if err != nil {
		return authority.Approval{}, err
	}
	if !added {
		return authority.Approval{}, fmt.Errorf("%w: request %s exists", workorders.ErrInvalid, id)
	}
	event, err := authority.ApprovalEvent(request, actor)
	if err != nil {
		return authority.Approval{}, err
	}
	_, err = tx.Append(event)
	return request, err
}

// keepRecovery records an act that ran or was refused, in the table that bounds the next and as an event.
func keepRecovery(tx ports.WriteTx, record authority.RecoveryRecord, actor ledger.Actor) error {
	if err := tx.Recoveries().Record(record); err != nil {
		return err
	}
	event, err := authority.RecoveryEvent(record, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// voidRecoveries closes the open recovery requests of a work order whose subject an act moved: a request
// is bound to what its subject was (FR-AUTH-2). One a member is granting is approved already, and stays.
func voidRecoveries(tx ports.WriteTx, workOrder string) error {
	rows, err := tx.Approvals().OfWorkOrder(workOrder)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Kind != authority.KindRecovery || !row.Open() {
			continue
		}
		next := row
		next.State, next.Reason = authority.ApprovalVoid, "its subject changed"
		if err := noteApproval(tx, next, row.State, approvalExpiry); err != nil {
			return err
		}
	}
	return nil
}

// grantRecovery runs the act a member approved, as that member's decision, once its subject is still what
// the request was bound to. A member is held to no policy, so a retry with no attempts left adds one, and a
// reset overrides the day. The request is spent on the act.
func grantRecovery(tx ports.WriteTx, request authority.Approval, actor ledger.Actor) error {
	act := authority.RecoveryAct(request.Subject)
	subject, err := readRecoverySubject(tx, act, request.WorkOrder)
	if err != nil {
		return err
	}
	detail := authority.RecoveryDetailOf(request)
	if err := performRecovery(tx, act, subject, detail.Reason, actor); err != nil {
		return err
	}
	used, err := request.Use(request.SubjectDigest, request.ID)
	if err != nil {
		return err
	}
	if err := noteApproval(tx, used, request.State, actor); err != nil {
		return err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return err
	}
	return keepRecovery(tx, authority.RecoveryRecord{
		ID: id, Act: act, WorkOrder: request.WorkOrder, Lane: subject.laneOf(act), Actor: authority.Principal(actor), Reason: detail.Reason,
		PolicyVersion: detail.PolicyVersion, Result: authority.RecoveryDone, Approval: request.ID, At: tx.Now(),
	}, actor)
}

// recoveryMoved says a recovery request's subject is no longer what it was bound to, which voids it.
func recoveryMoved(tx ports.WriteTx, request authority.Approval) (bool, error) {
	act := authority.RecoveryAct(request.Subject)
	subject, err := readRecoverySubject(tx, act, request.WorkOrder)
	if errors.Is(err, authority.ErrRecoveryMoved) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return subject.digest(act) != request.SubjectDigest, nil
}
