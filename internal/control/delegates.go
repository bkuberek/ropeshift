package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// What a delegate credential leaves in the installation's log. Each names the credential and holds no
// secret: no event carries a token or its digest.
const (
	EventDelegateCreated = "delegate.created"
	EventDelegateRevoked = "delegate.revoked"
	EventDelegateRemoved = "delegate.removed"
	// EventDelegateUsed records when a credential last acted, at most once a minute.
	EventDelegateUsed = "delegate.used"
	// EventSignedOutEverywhere records a member ending every session and credential at once.
	EventSignedOutEverywhere = "member.signed_out_everywhere"
	subjectDelegate          = "delegate"
)

// delegateTouchMs is how stale a credential's last use may read before a call records a new one.
const delegateTouchMs int64 = 60 * 1000

// DelegateGrant is a new credential. Token is the one time anyone sees it.
type DelegateGrant struct {
	Credential authority.DelegateCredential
	Token      string
}

// Delegates holds the use cases of delegate credentials (ADR-0024). Each is a member's alone, and each
// change commits in the installation, because a credential is the member's and not one project's.
type Delegates struct {
	store ports.Store
}

// NewDelegates returns the use cases over a Store.
func NewDelegates(store ports.Store) *Delegates { return &Delegates{store: store} }

func delegateEvent(eventType string, credential authority.DelegateCredential, actor ledger.Actor) (ledger.EventDraft, error) {
	data, err := json.Marshal(credential)
	if err != nil {
		return ledger.EventDraft{}, err
	}
	return ledger.EventDraft{Type: eventType, Actor: actor, Subject: ledger.Subject{Kind: subjectDelegate, ID: credential.ID}, Data: data}, nil
}

func appendDelegate(tx ports.SharedTx, eventType string, credential authority.DelegateCredential, actor ledger.Actor) error {
	event, err := delegateEvent(eventType, credential, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// Create makes a credential for the calling member, bound to one project a caller already found active.
// The token comes from the generator a session's does, and the ledger keeps only its digest.
func (d *Delegates) Create(ctx context.Context, scope ledger.WorkspaceID, request authority.DelegateRequest, actor ledger.Actor) (DelegateGrant, error) {
	if !authority.Allowed(actor, authority.ActionManageSessions) {
		return DelegateGrant{}, workorders.ErrForbidden
	}
	request.Member = actor.ID
	if err := request.Check(); err != nil {
		return DelegateGrant{}, err
	}
	if request.Project == "" {
		return DelegateGrant{}, fmt.Errorf("%w: a delegate names its project", authority.ErrInvalid)
	}
	token, err := newSecret()
	if err != nil {
		return DelegateGrant{}, err
	}
	grant := DelegateGrant{Token: token}
	err = d.store.Installation(scope).Write(ctx, "delegate.create", func(tx ports.SharedTx) error {
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		grant.Credential = authority.DelegateCredential{
			ID: authority.DelegateIDPrefix + id, Digest: digestOf(token), Member: actor.ID, Project: request.Project, Label: request.Label,
			Kind: authority.DelegateExternal, CreatedAt: tx.Now(), ExpiresAt: tx.Now() + request.ExpiresInMs,
		}
		if err := tx.Members().InsertDelegate(grant.Credential); err != nil {
			return err
		}
		return appendDelegate(tx, EventDelegateCreated, grant.Credential, actor)
	})
	if err != nil {
		return DelegateGrant{}, err
	}
	return grant, nil
}

// List reads the calling member's credentials, oldest first, with the database time they were read at.
func (d *Delegates) List(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]authority.DelegateCredential, int64, error) {
	if !authority.Allowed(actor, authority.ActionManageSessions) {
		return nil, 0, workorders.ErrForbidden
	}
	var out []authority.DelegateCredential
	var now int64
	err := d.store.Installation(scope).Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		if now, err = tx.Now(); err != nil {
			return err
		}
		out, err = tx.Members().Delegates(actor.ID)
		return err
	})
	return out, now, err
}

// own reads a credential of the calling member. Another member's reads as missing, so a member learns
// nothing of credentials that are not theirs.
func own(members ports.MemberReader, id string, actor ledger.Actor) (authority.DelegateCredential, error) {
	credential, err := members.Delegate(id)
	if err != nil {
		return credential, err
	}
	if credential.Member != actor.ID {
		return authority.DelegateCredential{}, fmt.Errorf("delegate credential %s: %w", id, ports.ErrNotFound)
	}
	return credential, nil
}

// Revoke ends one of the calling member's credentials for good. A credential already revoked stays as it is.
func (d *Delegates) Revoke(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (authority.DelegateCredential, error) {
	if !authority.Allowed(actor, authority.ActionManageSessions) {
		return authority.DelegateCredential{}, workorders.ErrForbidden
	}
	var credential authority.DelegateCredential
	err := d.store.Installation(scope).Write(ctx, "delegate.revoke", func(tx ports.SharedTx) error {
		var err error
		if credential, err = own(tx.Members(), id, actor); err != nil {
			return err
		}
		revoked, err := tx.Members().RevokeDelegate(id, tx.Now(), authority.DelegateEndRevoked)
		if err != nil || !revoked {
			return err
		}
		if credential.RevokedAt == 0 {
			credential.RevokedAt = tx.Now()
		}
		credential.EndedReason = authority.DelegateEndRevoked
		return appendDelegate(tx, EventDelegateRevoked, credential, actor)
	})
	return credential, err
}

// Remove deletes the record of one of the calling member's credentials, once it no longer acts.
func (d *Delegates) Remove(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionManageSessions) {
		return workorders.ErrForbidden
	}
	return d.store.Installation(scope).Write(ctx, "delegate.remove", func(tx ports.SharedTx) error {
		credential, err := own(tx.Members(), id, actor)
		if err != nil {
			return err
		}
		if credential.State(tx.Now()) == authority.DelegateActive {
			return fmt.Errorf("%w: revoke %s before removing it", authority.ErrDelegateActive, id)
		}
		if err := tx.Members().DropDelegate(id); err != nil {
			return err
		}
		return appendDelegate(tx, EventDelegateRemoved, credential, actor)
	})
}

// Authenticate reads the credential a token opens, against database time. A token that opens nothing
// active is refused, naming why. A credential that acts records its use when the last one it recorded is
// more than a minute old.
func (d *Delegates) Authenticate(ctx context.Context, scope ledger.WorkspaceID, token string) (authority.DelegateCredential, error) {
	if token == "" {
		return authority.DelegateCredential{}, fmt.Errorf("%w: no credential was presented", authority.ErrBadDelegate)
	}
	var credential authority.DelegateCredential
	var stale bool
	err := d.store.Installation(scope).Read(ctx, func(tx ports.SharedReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		if credential, err = activeDelegate(tx.Members(), token, now); err != nil {
			return err
		}
		stale = now-credential.LastUsedAt > delegateTouchMs
		return nil
	})
	if err != nil || !stale {
		return credential, err
	}
	err = d.store.Installation(scope).Write(ctx, "delegate.use", func(tx ports.SharedTx) error {
		current, err := activeDelegate(tx.Members(), token, tx.Now())
		if err != nil {
			return err
		}
		if tx.Now()-current.LastUsedAt <= delegateTouchMs {
			credential = current
			return nil
		}
		if err := tx.Members().TouchDelegate(current.ID, tx.Now()); err != nil {
			return err
		}
		current.LastUsedAt = tx.Now()
		credential = current
		return appendDelegate(tx, EventDelegateUsed, current, current.Actor())
	})
	return credential, err
}

func activeDelegate(members ports.MemberReader, token string, now int64) (authority.DelegateCredential, error) {
	credential, err := members.DelegateByDigest(digestOf(token))
	if errors.Is(err, ports.ErrNotFound) {
		return credential, fmt.Errorf("%w: this delegate credential is not known", authority.ErrBadDelegate)
	}
	if err != nil {
		return credential, err
	}
	switch credential.State(now) {
	case authority.DelegateRevoked:
		return credential, fmt.Errorf("%w: delegate credential %s was revoked", authority.ErrBadDelegate, credential.ID)
	case authority.DelegateExpired:
		return credential, fmt.Errorf("%w: delegate credential %s expired", authority.ErrBadDelegate, credential.ID)
	}
	return credential, nil
}

// SignedOut is what signing out everywhere ended: how many sessions, and which credentials it revoked.
// Ended adds the assistant credentials that had ended routinely, whose cards no longer stand either.
type SignedOut struct {
	Sessions  int64    `json:"sessions"`
	Delegates []string `json:"delegates"`
	Ended     []string `json:"-"`
}

// EndAll ends every session of the calling member and revokes every credential of theirs that still acts,
// in one installation transaction with one event (FR-AUTH-10). An assistant credential that ended routinely,
// with its generation or its session, now ends by the sign-out too, so no card any of the member's
// assistants prepared stands after it, an earlier generation's or a deleted conversation's included.
func (s *Sessions) EndAll(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) (SignedOut, error) {
	if !authority.Allowed(actor, authority.ActionManageSessions) {
		return SignedOut{}, workorders.ErrForbidden
	}
	ended := SignedOut{Delegates: []string{}}
	err := s.store.Installation(scope).Write(ctx, "member.sign_out_everywhere", func(tx ports.SharedTx) error {
		ended = SignedOut{Delegates: []string{}}
		var err error
		if ended.Sessions, err = tx.Members().DropMemberSessions(actor.ID); err != nil {
			return err
		}
		credentials, err := tx.Members().Delegates(actor.ID)
		if err != nil {
			return err
		}
		for _, credential := range credentials {
			active := credential.State(tx.Now()) == authority.DelegateActive
			routine := credential.Assistant() && credential.EndedRoutinely()
			if !active && !routine {
				continue
			}
			revoked, err := tx.Members().RevokeDelegate(credential.ID, tx.Now(), authority.DelegateEndSignedOut)
			if err != nil {
				return err
			}
			if !revoked {
				continue
			}
			if active {
				ended.Delegates = append(ended.Delegates, credential.ID)
			}
			ended.Ended = append(ended.Ended, credential.ID)
		}
		data, err := json.Marshal(ended)
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: EventSignedOutEverywhere, Actor: actor, Subject: ledger.Subject{Kind: subjectMember, ID: actor.ID}, Data: data})
		return err
	})
	if err != nil {
		return SignedOut{}, err
	}
	return ended, nil
}
