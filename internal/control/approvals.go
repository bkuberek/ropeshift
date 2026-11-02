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

// The operations of an approval.
const (
	opApprovalRaise  = "approval.raise"
	opApprovalDecide = "approval.decide"
	opApprovalUse    = "approval.use"
	opApprovalExpire = "approval.expire"
)

// ApprovalTTLMs is how long a request can stand with nobody deciding it. The design names a field
// and no value, and a week outlasts a person's absence and leaves nothing live for good.
const ApprovalTTLMs int64 = 7 * 24 * 60 * 60 * 1000

var approvalExpiry = ledger.Actor{Kind: ledger.ActorSystem, ID: "approval-expiry"}

// Approvals holds the use cases of a request bound to an artifact (FR-AUTH-1, FR-AUTH-2).
type Approvals struct {
	store ports.Store
}

// NewApprovals returns the use cases over a Store.
func NewApprovals(store ports.Store) *Approvals { return &Approvals{store: store} }

// noteApproval stores what a transition left, and tells whatever waits on it in the same write, so
// a request cannot end with a lane still parked on it.
func noteApproval(tx ports.WriteTx, next authority.Approval, from authority.ApprovalState, actor ledger.Actor) error {
	saved, err := tx.Approvals().Save(next, from)
	if err != nil {
		return err
	}
	if !saved {
		return fmt.Errorf("%w: it left %s", authority.ErrApprovalClosed, from)
	}
	crashpoint.Hit("approval.decide", "saved")
	event, err := authority.ApprovalEvent(next, actor)
	if err != nil {
		return err
	}
	if _, err := tx.Append(event); err != nil {
		return err
	}
	crashpoint.Hit("approval.decide", "recorded")
	if leavesApprovalWakes() {
		return nil
	}
	_, err = fireWakes(tx, "approval:"+next.ID, actor, parking.ApprovalDecided)
	return err
}

// raiseApproval records a request a worker raised for its lane. One that stands for this lane and
// this digest is that request, so a retry and a new worker raise nothing new. The principal is the
// one the worker's own record names, and no word of a lane's reaches it.
func raiseApproval(tx ports.WriteTx, request authority.Approval, actor ledger.Actor, stamp func(*ledger.EventDraft)) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	rows, err := tx.Approvals().OfLane(request.Lane)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Live() && row.Subject == request.Subject && row.SubjectDigest == request.SubjectDigest {
			return nil
		}
	}
	request.State, request.RaisedAt, request.ExpiresAt = authority.ApprovalPending, tx.Now(), tx.Now()+ApprovalTTLMs
	added, err := tx.Approvals().Insert(request)
	if err != nil || !added {
		return err
	}
	crashpoint.Hit("approval.raise", "inserted")
	event, err := authority.ApprovalEvent(request, actor)
	if err != nil {
		return err
	}
	stamp(&event)
	_, err = tx.Append(event)
	return err
}

// Raise records a request, for a caller already inside this ledger.
func (a *Approvals) Raise(ctx context.Context, scope ledger.WorkspaceID, request authority.Approval, actor ledger.Actor) error {
	return a.store.Write(ctx, scope, opApprovalRaise, func(tx ports.WriteTx) error {
		return raiseApproval(tx, request, actor, noStamp)
	})
}

// delegationVersion reads the version of the delegation policy in force, as currentPolicy does for
// decisions, so every decision names a version a member can read.
func delegationVersion(tx ports.WriteTx) (authority.PolicyVersion, error) {
	version, err := tx.Policies().InForce(authority.PolicyDelegation)
	if !errors.Is(err, ports.ErrNotFound) {
		return version, err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	if version, err = builtInDelegation(id, tx.Now()); err != nil {
		return authority.PolicyVersion{}, err
	}
	return version, insertPolicy(tx, version, policyActor)
}

func builtInDelegation(id string, createdAt int64) (authority.PolicyVersion, error) {
	draft := authority.DelegationDraft{Body: authority.BuiltInDelegationBody, Policy: authority.DelegationPolicy{Version: 1}}
	return authority.NewDelegationVersion(id, builtInPolicyAuthor, draft, createdAt)
}

// Decide applies a decision. A person needs a session, which a handler proved, and an orchestrator
// reaches only what a member delegated. Each check here repeats in the store where a statement can
// hold it, so the rule stands if this code is wrong.
func (a *Approvals) Decide(ctx context.Context, scope ledger.WorkspaceID, decision authority.Decision, actor ledger.Actor) (authority.Approval, error) {
	if !authority.Allowed(actor, authority.ActionDecideApproval) {
		return authority.Approval{}, workorders.ErrForbidden
	}
	var record authority.Approval
	err := a.store.Write(ctx, scope, opApprovalDecide, func(tx ports.WriteTx) error {
		row, err := tx.Approvals().Approval(decision.ID)
		if err != nil {
			return err
		}
		if row.Repeats(decision, actor) {
			record = row
			return nil
		}
		version, err := delegationVersion(tx)
		if err != nil {
			return err
		}
		if err := authority.CheckDelegated(row, version, actor); err != nil {
			return err
		}
		if record, err = row.Decide(decision, actor, version.Version, tx.Now()); err != nil {
			return err
		}
		return noteApproval(tx, record, row.State, actor)
	})
	if err != nil {
		return authority.Approval{}, err
	}
	return record, nil
}

// Check reads where a lane's call stands, with no write unless something changes. It is what a
// waiting hook polls, so a request nobody has decided costs a read.
func (a *Approvals) Check(ctx context.Context, scope ledger.WorkspaceID, use authority.ApprovalUse) (authority.ApprovalAnswer, error) {
	var answer authority.ApprovalAnswer
	var writes bool
	err := a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		rows, err := tx.Approvals().OfLane(use.Lane)
		if err != nil {
			return err
		}
		answer, writes = authority.Standing(rows, use)
		return nil
	})
	if err != nil || !writes {
		return answer, err
	}
	err = a.store.Write(ctx, scope, opApprovalUse, func(tx ports.WriteTx) error {
		var err error
		answer, err = useApproval(tx, use)
		return err
	})
	return answer, err
}

// useApproval spends a decision on one call, and voids what a changed call has left behind. A
// lane's approved request stands for what its reviewer saw: a call of that tool with any other
// input is a use that fails the comparison, so the request moves to `void` and must be raised again.
func useApproval(tx ports.WriteTx, use authority.ApprovalUse) (authority.ApprovalAnswer, error) {
	actor := ledger.Actor{Kind: ledger.ActorLane, ID: use.Lane}
	rows, err := tx.Approvals().OfLane(use.Lane)
	if err != nil {
		return authority.ApprovalAnswer{}, err
	}
	var answer authority.ApprovalAnswer
	for _, row := range rows {
		if row.Subject != use.Subject {
			continue
		}
		if row.SubjectDigest != use.Digest {
			if !row.Replaced(use.Generation) {
				continue
			}
			next := row
			next.State = authority.ApprovalVoid
			if err := noteApproval(tx, next, row.State, actor); err != nil {
				return authority.ApprovalAnswer{}, err
			}
			continue
		}
		if use.Peek || row.State != authority.ApprovalApproved {
			answer = authority.AnswerOf(row, use)
			continue
		}
		next, err := row.Use(use.Digest, use.Call)
		if err != nil {
			return authority.ApprovalAnswer{}, err
		}
		crashpoint.Hit("approval.use", "spent")
		if err := noteApproval(tx, next, row.State, actor); err != nil {
			return authority.ApprovalAnswer{}, err
		}
		answer = authority.AnswerOf(next, use)
	}
	return answer, nil
}

// approvalOutcomes reads how the requests of a generation that parked came out, for its next
// generation's first prompt, and the calls a decision still covers. A decision nobody has spent is
// owed whichever generation asked, because it covers the call until one is made.
func approvalOutcomes(reader ports.ApprovalReader, lane string, generation int64) (outcomes, calls []string, err error) {
	rows, err := reader.OfLane(lane)
	if err != nil {
		return nil, nil, err
	}
	for _, row := range rows {
		if call := row.Repeat(); call != "" {
			calls = append(calls, call)
		}
		if outcome := row.Outcome(); outcome != "" && row.Generation == generation {
			outcomes = append(outcomes, outcome)
		}
	}
	return outcomes, calls, nil
}

// answeredAtTerminal records what a session's own hooks showed: a person answered at the terminal.
// Nothing proved who, so this closes a request and grants nothing. A lane that says this of itself
// gets a closed request and no way to run anything.
func answeredAtTerminal(tx ports.WriteTx, lane string, answer authority.TerminalAnswer, actor ledger.Actor) error {
	rows, err := tx.Approvals().OfLane(lane)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !row.Live() || row.SubjectDigest != answer.Digest {
			continue
		}
		next := row
		next.State, next.Channel, next.Reason = authority.ApprovalAnswered, authority.ChannelTerminal, answer.Note
		if err := noteApproval(tx, next, row.State, actor); err != nil {
			return err
		}
	}
	return nil
}

// expireApprovals closes what a work order's lanes left live, in the transaction that ends them,
// because nothing can return to a session that will not run (FR-AUTH-7).
func expireApprovals(tx ports.WriteTx, workOrder string) error {
	rows, err := tx.Approvals().OfWorkOrder(workOrder)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := expireApproval(tx, row); err != nil {
			return err
		}
	}
	return nil
}

func expireApproval(tx ports.WriteTx, row authority.Approval) error {
	if !row.Live() {
		return nil
	}
	next := row
	next.State = authority.ApprovalExpired
	return noteApproval(tx, next, row.State, approvalExpiry)
}

// ExpireDue closes the requests past their time, each in a short transaction of its own. A list a
// tick read with no lock can be stale, so each is read again.
func (a *Approvals) ExpireDue(ctx context.Context, scope ledger.WorkspaceID) ([]string, error) {
	var due []string
	err := a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		due, err = tx.Approvals().Due(now)
		return err
	})
	if err != nil {
		return nil, err
	}
	var expired []string
	for _, id := range due {
		err := a.store.Write(ctx, scope, opApprovalExpire, func(tx ports.WriteTx) error {
			row, err := tx.Approvals().Approval(id)
			if err != nil {
				return err
			}
			return expireApproval(tx, row)
		})
		if err != nil {
			return expired, err
		}
		expired = append(expired, id)
	}
	return expired, nil
}

// List reads the requests of a workspace, oldest first, which a state narrows.
func (a *Approvals) List(ctx context.Context, scope ledger.WorkspaceID, state authority.ApprovalState) ([]authority.Approval, error) {
	var out []authority.Approval
	err := a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		rows, err := tx.Approvals().All()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if state == "" || row.State == state {
				out = append(out, row)
			}
		}
		return nil
	})
	return out, err
}

// Get reads one request.
func (a *Approvals) Get(ctx context.Context, scope ledger.WorkspaceID, id string) (authority.Approval, error) {
	var out authority.Approval
	err := a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Approvals().Approval(id)
		return err
	})
	return out, err
}

// PublishDelegation records a new version of the delegation policy, on the same terms as a decision
// policy: only a member can (FR-AUTH-6), each version is newer than the last, and the same body is
// a retry. A learned change cannot reach this, because it has no session (FR-EXP-6).
func (a *Approvals) PublishDelegation(ctx context.Context, scope ledger.WorkspaceID, draft authority.DelegationDraft, actor ledger.Actor) (authority.PolicyVersion, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return authority.PolicyVersion{}, workorders.ErrForbidden
	}
	var record authority.PolicyVersion
	err := a.store.Write(ctx, scope, "policy.publish", func(tx ports.WriteTx) error {
		current, err := delegationVersion(tx)
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
			return fmt.Errorf("%w: version %d is not newer than version %d", authority.ErrBadLanePolicy, draft.Policy.Version, current.Version)
		}
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		if record, err = authority.NewDelegationVersion(id, actor.ID, draft, tx.Now()); err != nil {
			return err
		}
		return insertPolicy(tx, record, actor)
	})
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return record, nil
}

// DelegationVersions lists the versions of the delegation policy, newest first. A workspace nothing
// has happened to yet still has the built-in one in force.
func (a *Approvals) DelegationVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error) {
	var versions []authority.PolicyVersion
	err := a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		versions, err = tx.Policies().Versions(authority.PolicyDelegation)
		return err
	})
	if err != nil || len(versions) != 0 {
		return versions, err
	}
	version, err := builtInDelegation(builtInPolicyAuthor, 0)
	if err != nil {
		return nil, err
	}
	return []authority.PolicyVersion{version}, nil
}

// approvalUpload stores a request a worker's lane raised. It travels as a fenced event, in journal
// order, so a lane that parks on it finds it here first, and a lane that lost its lease raises
// nothing.
func approvalUpload(tx ports.WriteTx, worker, id string, e ledger.UploadEvent, stamp func(*ledger.EventDraft)) error {
	var request authority.Approval
	if err := json.Unmarshal(e.Payload, &request); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if err := checkHolder(tx, e); err != nil {
		return err
	}
	if err := ranLane(tx, worker, e); err != nil {
		return err
	}
	request.Lane, request.WorkOrder, request.Generation = e.Lane, e.WorkOrder, e.Generation
	return raiseApproval(tx, request, ledger.Actor{Kind: ledger.ActorWorker, ID: worker}, stamp)
}

// answeredUpload stores what a tool's end showed.
func answeredUpload(tx ports.WriteTx, worker string, e ledger.UploadEvent) error {
	var answered authority.TerminalAnswer
	if err := json.Unmarshal(e.Payload, &answered); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if err := ranLane(tx, worker, e); err != nil {
		return err
	}
	return answeredAtTerminal(tx, e.Lane, answered, ledger.Actor{Kind: ledger.ActorWorker, ID: worker})
}

// UseApproval reads where a call of a lane this worker holds stands, and spends a decision on it. A
// lane reaches the ledger only through its worker, and the plane checks that this worker holds that
// lane at that fence, in that generation.
func (w *Workers) UseApproval(ctx context.Context, use authority.ApprovalUse) (authority.ApprovalAnswer, error) {
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		if err := w.checkSession(tx, use.Auth, use.Epoch); err != nil {
			return err
		}
		order, err := tx.WorkOrders().Get(use.WorkOrder)
		if err != nil {
			return err
		}
		if order.LeaseWorker != use.Auth.WorkerID || order.Fence != use.Fence {
			return &workorders.FencedError{Current: order.Fence, State: order.State}
		}
		row, err := tx.Lanes().Row(use.Lane)
		if err != nil {
			return err
		}
		generation, err := tx.Lanes().Generation(use.Lane)
		if err != nil {
			return err
		}
		if row.WorkOrder != use.WorkOrder || generation != use.Generation {
			return &workorders.FencedError{Current: order.Fence, State: order.State}
		}
		return nil
	})
	if err != nil {
		return authority.ApprovalAnswer{}, err
	}
	return NewApprovals(w.store).Check(ctx, w.scope, use)
}

// DecideApproval carries what this worker's orchestrator decided. An orchestrator reaches the
// ledger through its worker, as a lane does, and it is that worker's principal in the record.
func (w *Workers) DecideApproval(ctx context.Context, decision authority.WorkerDecision) (authority.Approval, error) {
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		return w.checkSession(tx, decision.Auth, decision.Epoch)
	})
	if err != nil {
		return authority.Approval{}, err
	}
	if err := w.heldToStanding(ctx, decision.Auth.WorkerID, authority.AutonomyStanding.MayWork); err != nil {
		return authority.Approval{}, err
	}
	actor := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: decision.Auth.WorkerID}
	return NewApprovals(w.store).Decide(ctx, w.scope, decision.Decision, actor)
}
