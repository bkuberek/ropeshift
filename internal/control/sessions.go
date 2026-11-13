package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// What sign-in leaves in the installation's log, because members are every project's. Each names a
// member and holds no secret, so a guess on the socket stays visible even though the operating system
// cannot stop one (FR-AUTH-5).
const (
	// EventSignedIn records one session.
	EventSignedIn      = "member.signed_in"
	eventSignedOut     = "member.signed_out"
	eventSignInRefused = "member.sign_in_refused"
	subjectMember      = "member"
)

// Who a record names when nobody has proved who they are yet.
var (
	firstEnrollment = ledger.Actor{Kind: ledger.ActorSystem, ID: "first-enrollment"}
	signInAttempt   = ledger.Actor{Kind: ledger.ActorSystem, ID: "sign-in-attempt"}
)

// SessionGrant is a new session. Token is the one time anyone sees it.
type SessionGrant struct {
	Token     string
	Member    string
	ExpiresAt int64
}

// Sessions turns what a person proved into a session a call can present.
type Sessions struct {
	store    ports.Store
	identity ports.IdentityProvider
}

// NewSessions returns the use cases over a Store and a provider.
func NewSessions(store ports.Store, identity ports.IdentityProvider) *Sessions {
	return &Sessions{store: store, identity: identity}
}

// SignIn checks a proof and issues a session. The provider runs outside any transaction,
// because a hash is slow on purpose. The ledger keeps only the token's digest.
func (s *Sessions) SignIn(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof, ttlMs int64) (SessionGrant, error) {
	member, err := s.identity.Authenticate(ctx, scope, proof)
	if errors.Is(err, authority.ErrBadProof) {
		if recorded := s.recordRefusal(ctx, scope); recorded != nil {
			return SessionGrant{}, recorded
		}
	}
	if err != nil {
		return SessionGrant{}, err
	}
	token, err := newSecret()
	if err != nil {
		return SessionGrant{}, err
	}
	grant := SessionGrant{Token: token, Member: member}
	err = s.store.Installation(scope).Write(ctx, "member.sign_in", func(tx ports.SharedTx) error {
		now := tx.Now()
		grant.ExpiresAt = now + authority.BoundTTL(ttlMs)
		if err := tx.Members().DropSessions(now); err != nil {
			return err
		}
		err := tx.Members().InsertSession(authority.Session{Digest: digestOf(token), Member: member, CreatedAt: now, ExpiresAt: grant.ExpiresAt})
		if err != nil {
			return err
		}
		crashpoint.Hit("member.sign_in", "issued")
		return appendSignIn(tx, EventSignedIn, ledger.Actor{Kind: ledger.ActorMember, ID: member}, grant.ExpiresAt)
	})
	if err != nil {
		return SessionGrant{}, err
	}
	return grant, nil
}

func (s *Sessions) recordRefusal(ctx context.Context, scope ledger.WorkspaceID) error {
	return s.store.Installation(scope).Write(ctx, "member.sign_in_refused", func(tx ports.SharedTx) error {
		return appendSignIn(tx, eventSignInRefused, signInAttempt, 0)
	})
}

func appendSignIn(tx ports.SharedTx, eventType string, actor ledger.Actor, expiresAt int64) error {
	data, err := json.Marshal(struct {
		ExpiresAt int64 `json:"expires_at,omitempty"`
	}{expiresAt})
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{Type: eventType, Actor: actor, Subject: ledger.Subject{Kind: subjectMember, ID: actor.ID}, Data: data})
	return err
}

// Member reads who a token signs in. Only a token the ledger knows, read against database
// time, makes a member actor, so an actor of that kind always stands on a live session.
func (s *Sessions) Member(ctx context.Context, scope ledger.WorkspaceID, token string) (ledger.Actor, error) {
	if token == "" {
		return ledger.Actor{}, authority.ErrNoSession
	}
	var actor ledger.Actor
	err := s.store.Installation(scope).Read(ctx, func(tx ports.SharedReadTx) error {
		session, err := tx.Members().Session(digestOf(token))
		if errors.Is(err, ports.ErrNotFound) {
			return fmt.Errorf("%w: this session is not known", authority.ErrNoSession)
		}
		if err != nil {
			return err
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		if !session.Live(now) {
			return fmt.Errorf("%w: this session expired", authority.ErrNoSession)
		}
		actor = ledger.Actor{Kind: ledger.ActorMember, ID: session.Member}
		return nil
	})
	if err != nil {
		return ledger.Actor{}, err
	}
	return actor, nil
}

// End closes a session for good, so its token opens nothing, even in a browser that keeps it.
func (s *Sessions) End(ctx context.Context, scope ledger.WorkspaceID, token string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionManageSessions) {
		return workorders.ErrForbidden
	}
	return s.store.Installation(scope).Write(ctx, "member.sign_out", func(tx ports.SharedTx) error {
		if err := tx.Members().DropSession(digestOf(token)); err != nil {
			return err
		}
		return appendSignIn(tx, eventSignedOut, actor, 0)
	})
}

// Enrolled says whether a passphrase is set, so a first run knows whether to ask for one.
func (s *Sessions) Enrolled(ctx context.Context, scope ledger.WorkspaceID) (bool, error) {
	return s.identity.Enrolled(ctx, scope)
}

// Enroll sets a passphrase. A caller with no session counts as the first enrollment, and
// the provider refuses that once a passphrase is set.
func (s *Sessions) Enroll(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof, token string) error {
	actor, err := s.Member(ctx, scope, token)
	if errors.Is(err, authority.ErrNoSession) {
		actor, err = firstEnrollment, nil
	}
	if err != nil {
		return err
	}
	return s.identity.Enroll(ctx, scope, proof, actor)
}
