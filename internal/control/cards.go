package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// EventDelegateExempted records a consent operation a delegate ran with no card, because the delegate
// policy in force exempts it, naming that version.
const EventDelegateExempted = "delegate.exempted"

// How a card binds to its subject (ADR-0024).
const (
	// BindRecord binds to a record as it stands: the revision of the newest event about it.
	BindRecord = "record"
	// BindRequest binds to a request a card decides: that request's digest and state.
	BindRequest = "request"
	// BindPolicy binds to the version of a policy kind in force.
	BindPolicy = "policy"
	// BindArguments binds to the arguments of an operation that creates its subject, which nothing can
	// change, so the card never goes void.
	BindArguments = "arguments"
)

// MaxOpenCards is how many open cards one delegate may hold in a project. A delegate past it waits for a
// member to decide one, so no agent can bury Needs you.
const MaxOpenCards = 20

// voidReason is what a card that went void says.
const voidReason = "The subject changed since the card was prepared."

// creating are the consent operations that create the record they name.
var creating = []string{authority.OpQuestionRecord, authority.OpDecisionRecord, authority.OpCategoryAdd}

// Binding is the subject a card is bound to, and how. For BindPolicy, ID is the policy kind.
type Binding struct {
	Mode      string
	Kind      string
	ID        string
	Arguments string
}

// Subject is the card's subject, as `<kind>:<id>`.
func (b Binding) Subject() string { return b.Kind + ":" + b.ID }

// BindingFor is how a card for an operation binds to the record it names. The server prepares a card with
// it, and a confirmation reads the same binding back from the card, so the two cannot drift.
func BindingFor(operation, kind, id string, arguments json.RawMessage) (Binding, error) {
	b := Binding{Mode: BindRecord, Kind: kind, ID: id}
	switch {
	case slices.Contains(creating, operation):
		canonical, err := authority.CanonicalInput(argumentsOrEmpty(arguments))
		if err != nil {
			return Binding{}, err
		}
		b.Mode, b.Arguments = BindArguments, canonical
	case operation == authority.OpApprovalDecide || operation == authority.OpApprovalDecideDelegated:
		b.Mode = BindRequest
	case kind == "policy":
		b.Mode = BindPolicy
	}
	return b, nil
}

func argumentsOrEmpty(arguments json.RawMessage) []byte {
	if len(strings.TrimSpace(string(arguments))) == 0 {
		return []byte("{}")
	}
	return arguments
}

// bindingOf reads a card's binding back from what it holds.
func bindingOf(card authority.Approval) (Binding, error) {
	detail, err := authority.CardDetailOf(card)
	if err != nil {
		return Binding{}, err
	}
	kind, id, ok := strings.Cut(card.Subject, ":")
	if !ok {
		return Binding{}, fmt.Errorf("%w: card %s names subject %q", authority.ErrBadApproval, card.ID, card.Subject)
	}
	return BindingFor(detail.Operation, kind, id, detail.Arguments)
}

// bindingReads are the reads a binding takes, from a read or a write transaction alike.
type bindingReads struct {
	events   func(ledger.Subject) ([]ledger.Event, error)
	approval func(string) (authority.Approval, error)
	inForce  func(authority.PolicyKind) (authority.PolicyVersion, error)
}

func (r bindingReads) digest(b Binding) (string, error) {
	switch b.Mode {
	case BindArguments:
		return authority.ArgumentsDigest(b.Arguments), nil
	case BindRequest:
		request, err := r.approval(b.ID)
		if err != nil {
			return "", err
		}
		return authority.RequestDigest(request), nil
	case BindPolicy:
		version, err := r.inForce(authority.PolicyKind(b.ID))
		if errors.Is(err, ports.ErrNotFound) {
			return authority.PolicyBinding(authority.PolicyKind(b.ID), builtInPolicyAuthor), nil
		}
		if err != nil {
			return "", err
		}
		return authority.PolicyBinding(authority.PolicyKind(b.ID), version.ID), nil
	case BindRecord:
		events, err := r.events(ledger.Subject{Kind: b.Kind, ID: b.ID})
		if err != nil {
			return "", err
		}
		var rev int64
		for _, event := range events {
			rev = max(rev, event.Rev)
		}
		return authority.RecordDigest(b.Kind, b.ID, rev), nil
	}
	return "", fmt.Errorf("%w: binding %q", authority.ErrBadApproval, b.Mode)
}

// autonomyReads reads the autonomy policy in force from the installation, the one kind a project's
// ledger does not hold, before a transaction that binds a card to it.
func (a *Approvals) autonomyReads(ctx context.Context, scope ledger.WorkspaceID, b Binding) (func(authority.PolicyKind) (authority.PolicyVersion, error), error) {
	if b.Mode != BindPolicy || authority.PolicyKind(b.ID) != authority.PolicyAutonomy {
		return nil, nil
	}
	var version authority.PolicyVersion
	var found error
	err := a.store.Installation(scope).Read(ctx, func(tx ports.SharedReadTx) error {
		version, found = tx.Policies().InForce(authority.PolicyAutonomy)
		if errors.Is(found, ports.ErrNotFound) {
			return nil
		}
		return found
	})
	if err != nil {
		return nil, err
	}
	return func(authority.PolicyKind) (authority.PolicyVersion, error) { return version, found }, nil
}

// Bind reads the digest a card for a binding holds now.
func (a *Approvals) Bind(ctx context.Context, scope ledger.WorkspaceID, b Binding) (string, error) {
	autonomy, err := a.autonomyReads(ctx, scope, b)
	if err != nil {
		return "", err
	}
	var digest string
	err = a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		reads := bindingReads{events: tx.EventsAbout, approval: tx.Approvals().Approval, inForce: tx.Policies().InForce}
		if autonomy != nil {
			reads.inForce = autonomy
		}
		var err error
		digest, err = reads.digest(b)
		return err
	})
	return digest, err
}

// StillBound says a card's subject is still what the card was bound to, by binding it again now.
func (a *Approvals) StillBound(ctx context.Context, scope ledger.WorkspaceID, card authority.Approval) (bool, error) {
	binding, err := bindingOf(card)
	if err != nil {
		return false, err
	}
	digest, err := a.Bind(ctx, scope, binding)
	if err != nil {
		return false, err
	}
	return digest == card.SubjectDigest, nil
}

// Prepare records a card a delegate's consent call made. It runs nothing, and it waits for a member with
// no time limit: a card never expires.
func (a *Approvals) Prepare(ctx context.Context, scope ledger.WorkspaceID, card authority.Approval, actor ledger.Actor) (authority.Approval, error) {
	if _, _, ok := authority.ActingFor(actor); !ok {
		return authority.Approval{}, workorders.ErrForbidden
	}
	err := a.store.Write(ctx, scope, "card.prepare", func(tx ports.WriteTx) error {
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		card.ID, card.Kind, card.RequestedBy = id, authority.KindConfirm, authority.Principal(actor)
		card.State, card.RaisedAt, card.ExpiresAt = authority.ApprovalPending, tx.Now(), 0
		if err := card.Validate(); err != nil {
			return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
		}
		open, err := openCardsOf(tx.Approvals(), card.RequestedBy)
		if err != nil {
			return err
		}
		if len(open) >= MaxOpenCards {
			return fmt.Errorf("%w: delegate %s holds %d open cards, the most one delegate holds in a project; a member decides one before it prepares another",
				workorders.ErrForbidden, actor.ID, MaxOpenCards)
		}
		added, err := tx.Approvals().Insert(card)
		if err != nil {
			return err
		}
		if !added {
			return fmt.Errorf("%w: card %s exists", workorders.ErrInvalid, card.ID)
		}
		event, err := authority.ApprovalEvent(card, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		return authority.Approval{}, err
	}
	return card, nil
}

// openCardsOf lists the cards a principal prepared that still wait for a member.
func openCardsOf(reader ports.ApprovalReader, principal string) ([]authority.Approval, error) {
	rows, err := reader.All()
	if err != nil {
		return nil, err
	}
	var open []authority.Approval
	for _, row := range rows {
		if row.Kind == authority.KindConfirm && row.Open() && row.RequestedBy == principal {
			open = append(open, row)
		}
	}
	return open, nil
}

// noLongerActs is what a card of a delegate whose credential was revoked or removed says.
const noLongerActs = "The delegate that prepared it no longer acts."

// requesterActs says the delegate that prepared a card still holds a credential that was not revoked. An
// expired one still stands behind what it prepared while it acted.
func requesterActs(members ports.MemberReader, card authority.Approval) (bool, error) {
	kind, id, _ := strings.Cut(card.RequestedBy, ":")
	_, credential, ok := authority.ActingFor(ledger.Actor{Kind: ledger.ActorKind(kind), ID: id})
	if !ok {
		return false, nil
	}
	record, err := members.Delegate(credential)
	if errors.Is(err, ports.ErrNotFound) {
		return false, nil
	}
	return err == nil && record.RevokedAt == 0, err
}

// VoidCardsOf voids the open cards a delegate prepared, once its credential was revoked: what an agent a
// member stopped trusting prepared stands no more.
func (a *Approvals) VoidCardsOf(ctx context.Context, scope ledger.WorkspaceID, delegate ledger.Actor, actor ledger.Actor) ([]string, error) {
	if !authority.Allowed(actor, authority.ActionManageSessions) {
		return nil, workorders.ErrForbidden
	}
	var voided []string
	err := a.store.Write(ctx, scope, "card.void_delegate", func(tx ports.WriteTx) error {
		voided = nil
		open, err := openCardsOf(tx.Approvals(), authority.Principal(delegate))
		if err != nil {
			return err
		}
		for _, row := range open {
			next := row
			next.State, next.Reason = authority.ApprovalVoid, noLongerActs
			if err := noteApproval(tx, next, row.State, actor); err != nil {
				return err
			}
			voided = append(voided, row.ID)
		}
		return nil
	})
	return voided, err
}

// Confirmed is what a member's word on a card did. Decided says this call moved the card out of pending,
// so its operation is this call's to run: a repeat of a decision already taken runs nothing again.
type Confirmed struct {
	Card    authority.Approval
	Decided bool
}

// Confirm applies a member's word on a card. Declining closes it. Confirming first binds the card's subject
// again, in the same transaction: a subject that moved voids the card, and one that stands approves it,
// which leaves the operation for the caller to run as the member.
func (a *Approvals) Confirm(ctx context.Context, scope ledger.WorkspaceID, decision authority.Decision, actor ledger.Actor, channel string) (Confirmed, error) {
	if !authority.Allowed(actor, authority.ActionConfirmCard) {
		return Confirmed{}, workorders.ErrForbidden
	}
	row, err := a.Get(ctx, scope, decision.ID)
	if err != nil {
		return Confirmed{}, err
	}
	binding, err := bindingOf(row)
	if err != nil {
		return Confirmed{}, err
	}
	autonomy, err := a.autonomyReads(ctx, scope, binding)
	if err != nil {
		return Confirmed{}, err
	}
	var out Confirmed
	var voided, revoked bool
	err = a.store.Write(ctx, scope, "card.confirm", func(tx ports.WriteTx) error {
		out, voided, revoked = Confirmed{}, false, false
		row, err := tx.Approvals().Approval(decision.ID)
		if err != nil {
			return err
		}
		if row.Kind != authority.KindConfirm {
			return fmt.Errorf("%w: %s is no card", authority.ErrBadApproval, row.ID)
		}
		if row.Repeats(decision, actor) {
			out.Card = row
			return nil
		}
		if !row.Open() {
			return fmt.Errorf("%w: it is %s", authority.ErrApprovalClosed, row.State)
		}
		if decision.Digest != row.SubjectDigest {
			return fmt.Errorf("%w: it is bound to %s", authority.ErrStaleDigest, row.SubjectDigest)
		}
		if decision.Allow {
			acts, err := requesterActs(tx.Members(), row)
			if err != nil {
				return err
			}
			if !acts {
				revoked = true
				out.Card = row
				out.Card.State, out.Card.Reason = authority.ApprovalVoid, noLongerActs
				return noteApproval(tx, out.Card, row.State, actor)
			}
			reads := bindingReads{events: tx.EventsAbout, approval: tx.Approvals().Approval, inForce: tx.Policies().InForce}
			if autonomy != nil {
				reads.inForce = autonomy
			}
			current, err := reads.digest(binding)
			if err != nil {
				return err
			}
			if current != row.SubjectDigest {
				voided = true
				out.Card = row
				out.Card.State, out.Card.Reason = authority.ApprovalVoid, voidReason
				return noteApproval(tx, out.Card, row.State, actor)
			}
		}
		decided, err := row.Decide(decision, actor, 0, tx.Now())
		if err != nil {
			return err
		}
		decided.Channel = channel
		out = Confirmed{Card: decided, Decided: true}
		return noteApproval(tx, decided, row.State, actor)
	})
	if err != nil {
		return Confirmed{}, err
	}
	if voided {
		return out, fmt.Errorf("%w: %s", authority.ErrStaleDigest, voidReason)
	}
	if revoked {
		return out, fmt.Errorf("%w: %s", authority.ErrApprovalClosed, noLongerActs)
	}
	return out, nil
}

// Finish records what became of a confirmed card's operation: used when the API ran it, and failed with
// the API's refusal as the reason when it did not.
func (a *Approvals) Finish(ctx context.Context, scope ledger.WorkspaceID, id string, ran bool, refusal string, actor ledger.Actor) (authority.Approval, error) {
	if !authority.Allowed(actor, authority.ActionConfirmCard) {
		return authority.Approval{}, workorders.ErrForbidden
	}
	var record authority.Approval
	err := a.store.Write(ctx, scope, "card.finish", func(tx ports.WriteTx) error {
		row, err := tx.Approvals().Approval(id)
		if err != nil {
			return err
		}
		if row.Kind != authority.KindConfirm || row.State != authority.ApprovalApproved {
			return fmt.Errorf("%w: it is %s", authority.ErrApprovalClosed, row.State)
		}
		record = row
		record.State = authority.ApprovalUsed
		if !ran {
			record.State, record.Reason = authority.ApprovalFailed, refusal
		}
		return noteApproval(tx, record, row.State, actor)
	})
	return record, err
}

// Void ends a confirmed card whose call no longer addresses what it was bound to, before the call runs.
func (a *Approvals) Void(ctx context.Context, scope ledger.WorkspaceID, id, reason string, actor ledger.Actor) (authority.Approval, error) {
	if !authority.Allowed(actor, authority.ActionConfirmCard) {
		return authority.Approval{}, workorders.ErrForbidden
	}
	var record authority.Approval
	err := a.store.Write(ctx, scope, "card.void", func(tx ports.WriteTx) error {
		row, err := tx.Approvals().Approval(id)
		if err != nil {
			return err
		}
		if row.Kind != authority.KindConfirm || row.State != authority.ApprovalApproved {
			return fmt.Errorf("%w: it is %s", authority.ErrApprovalClosed, row.State)
		}
		record = row
		record.State, record.Reason = authority.ApprovalVoid, reason
		return noteApproval(tx, record, row.State, actor)
	})
	return record, err
}

// CardRunMs is how long a confirmed card's call may run before a sweep takes it for interrupted. A call
// runs in the request that confirmed the card and takes far less, so a card approved this long ago with
// no outcome lost its call to a crash.
const CardRunMs int64 = 10 * 60 * 1000

// interrupted is what a card whose call a crash interrupted says.
const interrupted = "interrupted; check the audit"

var cardSweep = ledger.Actor{Kind: ledger.ActorSystem, ID: "card-sweep"}

// FailInterrupted fails each confirmed card approved more than bound milliseconds ago whose call recorded
// no outcome, because a crash between the approval and the outcome leaves it running for good. The audit
// says whether its call ran. Each is read again in a transaction of its own.
func (a *Approvals) FailInterrupted(ctx context.Context, scope ledger.WorkspaceID, bound int64) ([]string, error) {
	var running []string
	err := a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		running, err = tx.Approvals().Running(now - bound)
		return err
	})
	if err != nil {
		return nil, err
	}
	var failed []string
	for _, id := range running {
		var changed bool
		err := a.store.Write(ctx, scope, "card.interrupted", func(tx ports.WriteTx) error {
			row, err := tx.Approvals().Approval(id)
			if err != nil || row.Kind != authority.KindConfirm || row.State != authority.ApprovalApproved || row.DecidedAt > tx.Now()-bound {
				return err
			}
			next := row
			next.State, next.Reason = authority.ApprovalFailed, interrupted
			changed = true
			return noteApproval(tx, next, row.State, cardSweep)
		})
		if err != nil {
			return failed, err
		}
		if changed {
			failed = append(failed, id)
		}
	}
	return failed, nil
}

// Exempted records a consent operation a delegate ran with no card, naming the delegate policy version
// that exempted it.
func (a *Approvals) Exempted(ctx context.Context, scope ledger.WorkspaceID, subject ledger.Subject, detail authority.CardDetail, version int64, actor ledger.Actor) error {
	data, err := json.Marshal(struct {
		Operation     string `json:"operation"`
		Label         string `json:"label"`
		Method        string `json:"method"`
		Path          string `json:"path"`
		PolicyVersion int64  `json:"policy_version"`
	}{detail.Operation, detail.Label, detail.Method, detail.Path, version})
	if err != nil {
		return err
	}
	return a.store.Write(ctx, scope, "delegate.exempted", func(tx ports.WriteTx) error {
		_, err := tx.Append(ledger.EventDraft{Type: EventDelegateExempted, Actor: actor, Subject: subject, Data: data})
		return err
	})
}

// builtInDelegatePolicy is the version a project runs on before a member publishes one.
func builtInDelegatePolicy(id string, createdAt int64) (authority.PolicyVersion, error) {
	draft := authority.DelegatePolicyDraft{Body: authority.BuiltInDelegatePolicyBody, Policy: authority.BuiltInDelegatePolicy()}
	return authority.NewDelegatePolicyVersion(id, builtInPolicyAuthor, draft, createdAt)
}

// DelegatePolicyInForce reads the delegate policy a project runs on now, and the built-in one when no member
// has published any. It writes nothing.
func (a *Approvals) DelegatePolicyInForce(ctx context.Context, scope ledger.WorkspaceID) (authority.PolicyVersion, authority.DelegatePolicy, error) {
	var version authority.PolicyVersion
	err := a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		version, err = tx.Policies().InForce(authority.PolicyDelegate)
		return err
	})
	if errors.Is(err, ports.ErrNotFound) {
		version, err = builtInDelegatePolicy(builtInPolicyAuthor, 0)
	}
	if err != nil {
		return authority.PolicyVersion{}, authority.DelegatePolicy{}, err
	}
	policy, err := authority.ReadDelegatePolicy(version)
	return version, policy, err
}

// PublishDelegatePolicy records a new version of the delegate policy: only a member can, each version is
// newer than the last, the same body is a retry, and a version that names anything but an exemptable
// consent operation is refused, naming each.
func (a *Approvals) PublishDelegatePolicy(ctx context.Context, scope ledger.WorkspaceID, draft authority.DelegatePolicyDraft, actor ledger.Actor) (authority.PolicyVersion, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return authority.PolicyVersion{}, workorders.ErrForbidden
	}
	if err := draft.Policy.Check(); err != nil {
		return authority.PolicyVersion{}, err
	}
	var record authority.PolicyVersion
	err := a.store.Write(ctx, scope, "policy.publish", func(tx ports.WriteTx) error {
		current, err := tx.Policies().InForce(authority.PolicyDelegate)
		if errors.Is(err, ports.ErrNotFound) {
			id, err := newID(tx.Now())
			if err != nil {
				return err
			}
			if current, err = builtInDelegatePolicy(id, tx.Now()); err != nil {
				return err
			}
			if err := insertPolicy(tx, current, policyActor); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if current.Version == draft.Policy.Version && current.Digest == authority.PolicyDigest(draft.Body) {
			record = current
			return nil
		}
		if draft.Policy.Version <= current.Version {
			return fmt.Errorf("%w: version %d is not newer than version %d", authority.ErrInvalid, draft.Policy.Version, current.Version)
		}
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		if record, err = authority.NewDelegatePolicyVersion(id, actor.ID, draft, tx.Now()); err != nil {
			return err
		}
		return insertPolicy(tx, record, actor)
	})
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return record, nil
}

// DelegatePolicyVersions lists the versions of the delegate policy, newest first, with the built-in one when
// no member has published any.
func (a *Approvals) DelegatePolicyVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error) {
	var versions []authority.PolicyVersion
	err := a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		versions, err = tx.Policies().Versions(authority.PolicyDelegate)
		return err
	})
	if err != nil || len(versions) != 0 {
		return versions, err
	}
	version, err := builtInDelegatePolicy(builtInPolicyAuthor, 0)
	if err != nil {
		return nil, err
	}
	return []authority.PolicyVersion{version}, nil
}
