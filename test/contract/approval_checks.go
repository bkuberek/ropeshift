package contract

import (
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// ApprovalChecks are what the ledger promises about requests (FR-AUTH-1, FR-AUTH-2, FR-AUTH-6,
// FR-AUTH-7). The first ones write past every use case, because the refusal is the store's.
var ApprovalChecks = []Check{
	{"AP1", "the store refuses a decision by the principal that asked", checkStoreRefusesSelfDecision},
	{"AP2", "the store refuses a decision by a kind that decides nothing", checkStoreRefusesKind},
	{"AP3", "the store refuses a decision that names another digest", checkStoreRefusesDigest},
	{"AP4", "the store refuses an approval with nobody behind it", checkStoreRefusesNobody},
	{"AP5", "a decision reaches a lane's wake in its own write", checkDecisionFiresWake},
	{"AP6", "a decision runs one call, and a replay runs none", checkOneCall},
	{"AP7", "a changed call voids what was approved, and a new request starts", checkChangedCallVoids},
	{"AP8", "a request ends with its lane", checkRequestEndsWithLane},
	{"AP9", "what one lane got, another cannot use", checkOtherLane},
	{"AP10", "a worker that lost its lease spends nothing", checkFencedSpendsNothing},
	{"AP11", "an orchestrator decides only a declared class", checkDelegatedClass},
	{"AP12", "an orchestrator's own request goes to a member", checkOrchestratorsOwnRequest},
	{"AP13", "only a member changes the delegation policy", checkOnlyMemberDelegates},
	{"AP14", "a decision names the policy version in force", checkDecisionNamesVersion},
	{"AP15", "every request replays from the log", checkApprovalsReplay},
	{"AP16", "a request past its time expires, and wakes what waits on it", checkExpiryWakes},
	{"AP17", "a write moves a request only from the state it read", checkSaveComparesItsState},
	{"AP18", "a worker session that ended reads no answer", checkEndedSessionReadsNothing},
	{"AP19", "a decision sent twice is one decision", checkRetryDecidesOnce},
	{"AP20", "a worker reads only the lane of the work order it holds", checkLaneOfThatWorkOrder},
}

var (
	theMember     = ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	aWorkerAsOrch = ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "worker-1"}
)

const gatedInput = `{"command":"git tag v1"}`

func aCall(input string) authority.ToolCall {
	call, err := authority.ReadToolCall("Bash", []byte(input))
	if err != nil {
		panic(err)
	}
	return call
}

// raised is a request of this rig's lane, as a worker's upload stores it.
func (s *statusRig) raised(input string) (authority.Approval, error) {
	call := aCall(input)
	request := authority.Approval{
		ID: s.r.ID("approval"), Kind: authority.KindToolCall, Subject: call.Tool, SubjectDigest: call.Digest, Input: call.Input,
		Command: call.Command, Path: call.Path, Summary: call.Summary, RequestedBy: "lane:" + s.lane,
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return request, err
	}
	reply, err := s.uploadAt(1, ledger.UploadApprovalRaised, raw)
	if err != nil {
		return request, err
	}
	if len(reply.Rejected) != 0 {
		return request, Violatedf("the request was refused: %+v", reply.Rejected)
	}
	return s.approval(request.ID)
}

func (s *statusRig) approval(id string) (authority.Approval, error) {
	return control.NewApprovals(s.store).Get(s.r.Ctx, s.r.Scope, id)
}

func (s *statusRig) decide(a authority.Approval, allow bool, actor ledger.Actor) (authority.Approval, error) {
	return control.NewApprovals(s.store).Decide(s.r.Ctx, s.r.Scope, authority.Decision{ID: a.ID, Digest: a.SubjectDigest, Allow: allow}, actor)
}

func (s *statusRig) use(digest, call string) (authority.ApprovalAnswer, error) {
	return s.plane.UseApproval(s.r.Ctx, authority.ApprovalUse{
		Auth: s.auth, Epoch: s.epoch, WorkOrder: s.grant.WorkOrder, Fence: s.grant.Fence, Lane: s.lane, Generation: 1,
		Subject: "Bash", Digest: digest, Call: call,
	})
}

// saveDirectly writes a record past every use case. A use case can refuse this, and a store must.
func (s *statusRig) saveDirectly(next authority.Approval) error {
	return s.store.Write(s.r.Ctx, s.r.Scope, "test.approval", func(tx ports.WriteTx) error {
		if _, err := tx.Approvals().Save(next, authority.ApprovalPending); err != nil {
			return err
		}
		event, err := authority.ApprovalEvent(next, ledger.Actor{Kind: ledger.ActorSystem, ID: "test"})
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

func (s *statusRig) refusedAtStore(change func(*authority.Approval)) error {
	a, err := s.raised(fmt.Sprintf(`{"command":"git tag v%d"}`, s.seq))
	if err != nil {
		return err
	}
	next := a
	next.State, next.DecidedBy, next.DecidedKind, next.DecidedDigest = authority.ApprovalApproved, "member:m1", "member", a.SubjectDigest
	change(&next)
	if err := s.saveDirectly(next); err == nil {
		return Violatedf("the store took %+v", next)
	}
	after, err := s.approval(a.ID)
	if err != nil {
		return err
	}
	if after.State != authority.ApprovalPending {
		return Violatedf("the request reads %s", after.State)
	}
	return nil
}

// A principal of a kind that may decide, on its own request, is the one case only this rule stops.
func checkStoreRefusesSelfDecision(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	call := aCall(gatedInput)
	own := authority.Approval{
		ID: r.ID("approval"), Lane: s.lane, WorkOrder: s.grant.WorkOrder, Generation: 1, Kind: authority.KindToolCall, Subject: call.Tool,
		SubjectDigest: call.Digest, Input: call.Input, Summary: call.Summary, RequestedBy: authority.Principal(aWorkerAsOrch),
	}
	if err := control.NewApprovals(s.store).Raise(r.Ctx, r.Scope, own, aWorkerAsOrch); err != nil {
		return err
	}
	next := own
	next.State, next.DecidedBy, next.DecidedKind = authority.ApprovalApproved, own.RequestedBy, string(aWorkerAsOrch.Kind)
	next.DecidedDigest = own.SubjectDigest
	if err := s.saveDirectly(next); err == nil {
		return Violatedf("the store took a self decision")
	}
	return nil
}

func checkStoreRefusesKind(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := s.refusedAtStore(func(a *authority.Approval) { a.DecidedBy, a.DecidedKind = "lane:other", "lane" }); err != nil {
		return err
	}
	return s.refusedAtStore(func(a *authority.Approval) { a.DecidedBy, a.DecidedKind = "lane:other", "member" })
}

func checkStoreRefusesDigest(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	return s.refusedAtStore(func(a *authority.Approval) { a.DecidedDigest = aCall(`{"command":"git tag v2"}`).Digest })
}

func checkStoreRefusesNobody(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	return s.refusedAtStore(func(a *authority.Approval) { a.DecidedBy, a.DecidedKind, a.DecidedDigest = "", "", "" })
}

func checkDecisionFiresWake(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	a, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	wake := "lane-wake"
	if err := s.parkOn(wake, a); err != nil {
		return err
	}
	if err := s.wakeFired(wake, false); err != nil {
		return err
	}
	if _, err := s.decide(a, false, theMember); err != nil {
		return err
	}
	return s.wakeFired(wake, true)
}

// parkOn parks the lane on one request.
func (s *statusRig) parkOn(wake string, a authority.Approval) error {
	raw, err := json.Marshal(map[parking.Kind]string{parking.ApprovalDecided: a.ID})
	if err != nil {
		return err
	}
	if reply, err := s.parkingUpload(wake, string(raw)); err != nil || len(reply.Rejected) != 0 {
		return fmt.Errorf("parking: %+v, %w", reply, err)
	}
	if reply, err := s.parkedUpload(wake, true); err != nil || len(reply.Rejected) != 0 {
		return fmt.Errorf("parked: %+v, %w", reply, err)
	}
	return nil
}

func checkOneCall(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	a, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	if answer, err := s.use(a.SubjectDigest, "call-1"); err != nil || answer.Allowed || answer.State != authority.ApprovalPending {
		return Violatedf("an undecided request answers %+v, %v", answer, err)
	}
	if _, err := s.decide(a, true, theMember); err != nil {
		return err
	}
	if answer, err := s.use(a.SubjectDigest, "call-1"); err != nil || !answer.Allowed {
		return Violatedf("the decided call answers %+v, %v", answer, err)
	}
	if answer, err := s.use(a.SubjectDigest, "call-1"); err != nil || !answer.Allowed {
		return Violatedf("a call whose answer was lost hears %+v, %v", answer, err)
	}
	if answer, err := s.use(a.SubjectDigest, "call-2"); err != nil || answer.Allowed {
		return Violatedf("a replay answers %+v, %v", answer, err)
	}
	if _, err := s.decide(a, true, ledger.Actor{Kind: ledger.ActorMember, ID: "member-2"}); !errors.Is(err, authority.ErrApprovalClosed) {
		return Violatedf("a second decision: %v", err)
	}
	return nil
}

func checkChangedCallVoids(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	a, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	if _, err := s.decide(a, true, theMember); err != nil {
		return err
	}
	changed := aCall(`{"command":"git tag v2"}`)
	if answer, err := s.use(changed.Digest, "call-2"); err != nil || answer.Allowed {
		return Violatedf("a changed call answers %+v, %v", answer, err)
	}
	after, err := s.approval(a.ID)
	if err != nil {
		return err
	}
	if after.State != authority.ApprovalVoid {
		return Violatedf("the old approval reads %s", after.State)
	}
	if answer, err := s.use(a.SubjectDigest, "call-3"); err != nil || answer.Allowed {
		return Violatedf("a void approval answers %+v, %v", answer, err)
	}
	next, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	if next.ID == a.ID || next.State != authority.ApprovalPending {
		return Violatedf("the request raised again reads %+v", next)
	}
	return nil
}

func checkRequestEndsWithLane(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	a, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	result, err := json.Marshal(workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeFailed, Reason: "crashed"}})
	if err != nil {
		return err
	}
	if reply, err := s.uploadAt(1, ledger.UploadLaneEnded, result); err != nil || len(reply.Rejected) != 0 {
		return fmt.Errorf("end: %+v, %w", reply, err)
	}
	after, err := s.approval(a.ID)
	if err != nil {
		return err
	}
	if after.State != authority.ApprovalExpired {
		return Violatedf("a request of a lane that ended reads %s", after.State)
	}
	return nil
}

func checkOtherLane(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	a, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	if _, err := s.decide(a, true, theMember); err != nil {
		return err
	}
	other, err := newStatusRig(r)
	if err != nil {
		return err
	}
	answer, err := other.use(a.SubjectDigest, "call-1")
	if err != nil {
		return err
	}
	if answer.Allowed || answer.State != "" {
		return Violatedf("another lane's call answers %+v", answer)
	}
	return nil
}

func checkFencedSpendsNothing(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	a, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	if _, err := s.decide(a, true, theMember); err != nil {
		return err
	}
	stale := *s
	stale.grant.Fence--
	var fenced *workorders.FencedError
	if _, err := stale.use(a.SubjectDigest, "call-1"); !errors.As(err, &fenced) {
		return Violatedf("a stale fence: %v", err)
	}
	after, err := s.approval(a.ID)
	if err != nil {
		return err
	}
	if after.State != authority.ApprovalApproved {
		return Violatedf("the approval reads %s", after.State)
	}
	return nil
}

// delegating publishes a policy with one class of each kind.
func (s *statusRig) delegating(version int64) (authority.PolicyVersion, error) {
	policy := authority.DelegationPolicy{Version: version, Classes: []authority.DelegationClass{{Tool: "Bash", CommandPrefix: "go test"}}}
	body := fmt.Sprintf("[delegation_policy]\nversion = %d\n\n[[delegation_policy.classes]]\ntool = \"Bash\"\ncommand_prefix = \"go test\"\n", version)
	return control.NewApprovals(s.store).PublishDelegation(s.r.Ctx, s.r.Scope, authority.DelegationDraft{Body: body, Policy: policy}, theMember)
}

func checkDelegatedClass(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if _, err := s.delegating(2); err != nil {
		return err
	}
	outside, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	if _, err := s.decide(outside, true, aWorkerAsOrch); !errors.Is(err, authority.ErrNotDelegated) {
		return Violatedf("a request outside every class: %v", err)
	}
	inside, err := s.raised(`{"command":"go test ./..."}`)
	if err != nil {
		return err
	}
	decided, err := s.decide(inside, true, aWorkerAsOrch)
	if err != nil {
		return err
	}
	if decided.State != authority.ApprovalApproved || decided.DecidedBy != authority.Principal(aWorkerAsOrch) {
		return Violatedf("decided %+v", decided)
	}
	chained, err := s.raised(`{"command":"go test ./... && rm -rf ."}`)
	if err != nil {
		return err
	}
	if _, err := s.decide(chained, true, aWorkerAsOrch); !errors.Is(err, authority.ErrNotDelegated) {
		return Violatedf("a chained command: %v", err)
	}
	return nil
}

func checkOrchestratorsOwnRequest(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if _, err := s.delegating(2); err != nil {
		return err
	}
	call := aCall(`{"command":"go test ./..."}`)
	own := authority.Approval{
		ID: s.r.ID("approval"), Lane: s.lane, WorkOrder: s.grant.WorkOrder, Generation: 1, Kind: authority.KindToolCall, Subject: call.Tool,
		SubjectDigest: call.Digest, Input: call.Input, Command: call.Command, Summary: call.Summary, RequestedBy: authority.Principal(aWorkerAsOrch),
	}
	approvals := control.NewApprovals(s.store)
	if err := approvals.Raise(s.r.Ctx, s.r.Scope, own, aWorkerAsOrch); err != nil {
		return err
	}
	if _, err := s.decide(own, true, aWorkerAsOrch); !errors.Is(err, authority.ErrSelfApproval) {
		return Violatedf("an orchestrator on its own request: %v", err)
	}
	if _, err := s.decide(own, true, theMember); err != nil {
		return Violatedf("a member on an orchestrator's request: %v", err)
	}
	return nil
}

func checkOnlyMemberDelegates(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	draft := authority.DelegationDraft{Body: "[delegation_policy]\nversion = 2\n", Policy: authority.DelegationPolicy{Version: 2}}
	for _, actor := range []ledger.Actor{aWorkerAsOrch, {Kind: ledger.ActorLane, ID: s.lane}, {Kind: ledger.ActorWorker, ID: "w1"}} {
		if _, err := control.NewApprovals(s.store).PublishDelegation(s.r.Ctx, s.r.Scope, draft, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s published a policy: %v", actor.Kind, err)
		}
	}
	return nil
}

func checkDecisionNamesVersion(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if _, err := s.delegating(5); err != nil {
		return err
	}
	a, err := s.raised(`{"command":"go test ./..."}`)
	if err != nil {
		return err
	}
	decided, err := s.decide(a, true, aWorkerAsOrch)
	if err != nil {
		return err
	}
	if decided.PolicyVersion != 5 {
		return Violatedf("the decision names version %d", decided.PolicyVersion)
	}
	return nil
}

func checkApprovalsReplay(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	a, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	if _, err := s.decide(a, true, theMember); err != nil {
		return err
	}
	if _, err := s.use(a.SubjectDigest, "call-1"); err != nil {
		return err
	}
	if _, err := s.raised(`{"command":"git tag v2"}`); err != nil {
		return err
	}
	events, err := r.Events(s.store, r.Scope)
	if err != nil {
		return err
	}
	book, err := authority.ReplayApprovals(events)
	if err != nil {
		return err
	}
	rows, err := control.NewApprovals(s.store).List(r.Ctx, r.Scope, "")
	if err != nil {
		return err
	}
	if len(rows) != len(book) {
		return Violatedf("%d requests, the log replays to %d", len(rows), len(book))
	}
	for _, row := range rows {
		if book[row.ID] != row {
			return Violatedf("request %+v disagrees with its log: %+v", row, book[row.ID])
		}
	}
	return nil
}

func checkExpiryWakes(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	a, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	const wake, margin = "lane-wake", int64(60_000)
	if err := s.parkOn(wake, a); err != nil {
		return err
	}
	approvals := control.NewApprovals(s.store)
	r.DB.ShiftClock(control.ApprovalTTLMs - margin)
	early, err := approvals.ExpireDue(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(early) != 0 {
		return Violatedf("a request expired inside its time: %v", early)
	}
	r.DB.ShiftClock(2 * margin)
	due, err := approvals.ExpireDue(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(due) != 1 || due[0] != a.ID {
		return Violatedf("past its time, %v expired", due)
	}
	row, err := s.approval(a.ID)
	if err != nil {
		return err
	}
	if row.State != authority.ApprovalExpired {
		return Violatedf("a request past its time is %s", row.State)
	}
	return s.wakeFired(wake, true)
}

// checkLaneOfThatWorkOrder holds a look to the lane of the work order a worker named, so a lease on
// one work order reaches no other lane's decision.
func checkLaneOfThatWorkOrder(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	a, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	if _, err := s.decide(a, true, theMember); err != nil {
		return err
	}
	holder, err := newStatusRig(r)
	if err != nil {
		return err
	}
	holder.lane = s.lane
	var fenced *workorders.FencedError
	if _, err := holder.use(a.SubjectDigest, "call-1"); !errors.As(err, &fenced) {
		return Violatedf("a worker read a lane of a work order it does not hold: %v", err)
	}
	row, err := s.approval(a.ID)
	if err != nil {
		return err
	}
	if row.State != authority.ApprovalApproved {
		return Violatedf("the approval reads %s", row.State)
	}
	return nil
}

// checkEndedSessionReadsNothing holds a look at a call to the worker session that makes it, because
// the answer it hears lets a tool run.
func checkEndedSessionReadsNothing(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	a, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	if _, err := s.decide(a, true, theMember); err != nil {
		return err
	}
	ended := *s
	ended.epoch--
	if _, err := ended.use(a.SubjectDigest, "call-1"); !errors.Is(err, ledger.ErrStaleEpoch) {
		return Violatedf("a session that ended: %v", err)
	}
	row, err := s.approval(a.ID)
	if err != nil {
		return err
	}
	if row.State != authority.ApprovalApproved {
		return Violatedf("the approval reads %s", row.State)
	}
	return nil
}

// checkRetryDecidesOnce reads a decision sent twice as the one decision, because an answer can be
// lost on its way back to a person.
func checkRetryDecidesOnce(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	a, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	first, err := s.decide(a, true, theMember)
	if err != nil {
		return err
	}
	again, err := s.decide(a, true, theMember)
	if err != nil {
		return fmt.Errorf("a retry: %w", err)
	}
	if again.DecidedAt != first.DecidedAt || again.State != authority.ApprovalApproved {
		return Violatedf("a retry reads %+v after %+v", again, first)
	}
	if _, err := s.decide(a, false, theMember); !errors.Is(err, authority.ErrApprovalClosed) {
		return Violatedf("a change of mind reads as a retry: %v", err)
	}
	return nil
}

// checkSaveComparesItsState holds a write to the state it read, so two writers that read one open
// request cannot both decide it.
func checkSaveComparesItsState(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	a, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	if _, err := s.decide(a, true, theMember); err != nil {
		return err
	}
	late, err := a.Decide(authority.Decision{ID: a.ID, Digest: a.SubjectDigest}, theMember, 0, 1)
	if err != nil {
		return err
	}
	errNothingWritten := errors.New("nothing was written")
	err = s.store.Write(r.Ctx, r.Scope, "approval.late", func(tx ports.WriteTx) error {
		saved, err := tx.Approvals().Save(late, authority.ApprovalPending)
		if err != nil {
			return err
		}
		if saved {
			return Violatedf("a write moved a request from a state it had left")
		}
		return errNothingWritten
	})
	if !errors.Is(err, errNothingWritten) {
		return err
	}
	row, err := s.approval(a.ID)
	if err != nil {
		return err
	}
	if row.State != authority.ApprovalApproved {
		return Violatedf("an approved request is %s after a late write", row.State)
	}
	return nil
}
