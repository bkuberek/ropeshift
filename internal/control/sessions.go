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
	// EventSessionsEnded records the other sessions of a member that a change of the passphrase ended.
	EventSessionsEnded = "member.sessions_ended"
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
	// startsOrchestrators has the first passphrase ask for the orchestrator of each registered worker that has none.
	startsOrchestrators bool
}

// NewSessions returns the use cases over a Store and a provider.
func NewSessions(store ports.Store, identity ports.IdentityProvider) *Sessions {
	return &Sessions{store: store, identity: identity}
}

// StartingOrchestrators sets whether the first passphrase asks for the orchestrator of each registered worker that has
// none, in the transaction that stores it. A worker's session asks for none until a member exists, so the first
// passphrase is what starts the orchestrator of a worker that connected before it. A daemon sets this as it sets
// WorkerOptions.StartsOrchestrator, and a test harness that starts an orchestrator only when a test asks leaves it off.
func (s *Sessions) StartingOrchestrators(on bool) *Sessions {
	s.startsOrchestrators = on
	return s
}

// startOrchestrators is what runs in the transaction that stores the first passphrase, or nil when these use cases
// start no orchestrator. A worker that has a record of its orchestrator keeps it.
func (s *Sessions) startOrchestrators() func(ports.SharedTx, string) error {
	if !s.startsOrchestrators {
		return nil
	}
	return func(tx ports.SharedTx, _ string) error {
		workers, err := tx.Workers().List()
		if err != nil {
			return err
		}
		for _, worker := range workers {
			if err := askForOrchestrator(tx, worker.ID, firstEnrollment); err != nil {
				return err
			}
		}
		return nil
	}
}

// SignIn checks a proof and issues a session. The provider checks outside any transaction, because a hash is slow on
// purpose, and issues the session in a write that finds the passphrase it checked still in force, so a change of the
// passphrase that landed after the check leaves no session it did not end: the sign-in is refused with
// authority.ErrPassphraseChanged, which is no wrong guess and is not on record as a refusal. The ledger keeps only the
// token's digest.
func (s *Sessions) SignIn(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof, ttlMs int64) (SessionGrant, error) {
	var grant SessionGrant
	member, err := s.identity.Authenticate(ctx, scope, proof, func(tx ports.SharedTx, member string) error {
		token, err := newSecret()
		if err != nil {
			return err
		}
		now := tx.Now()
		grant = SessionGrant{Token: token, Member: member, ExpiresAt: now + authority.BoundTTL(ttlMs)}
		if err := tx.Members().DropSessions(now); err != nil {
			return err
		}
		err = tx.Members().InsertSession(authority.Session{Digest: digestOf(token), Member: member, CreatedAt: now, ExpiresAt: grant.ExpiresAt})
		if err != nil {
			return err
		}
		crashpoint.Hit("member.sign_in", "issued")
		return appendSignIn(tx, EventSignedIn, ledger.Actor{Kind: ledger.ActorMember, ID: member}, grant.ExpiresAt)
	})
	if errors.Is(err, authority.ErrBadProof) {
		// The guess was wrong whether or not its client is still there, so the record is made as though it were: a
		// client that leaves after the check must not turn a refusal into a failure to write one. A record that
		// fails to be written comes back beside the wrong passphrase, which stays the answer.
		if recorded := s.recordRefusal(context.WithoutCancel(ctx), scope); recorded != nil {
			return SessionGrant{}, errors.Join(err, recorded)
		}
	}
	if err != nil {
		return SessionGrant{}, err
	}
	if grant.Token == "" || grant.Member != member {
		return SessionGrant{}, fmt.Errorf("the identity provider proved member %q and issued no session for it", member)
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

// PassphraseChange is what a change of the passphrase did. Token is the new token of the session the change came
// from, which replaces the old one, whose digest the change dropped, and which a caller hands to its own client and
// to no one else. ExpiresAt is the end the session already had, since a change must not lengthen it, and LifeMs what
// is left of it by the ledger's clock. SessionsEnded counts the member's other sessions that ended, and Delegates the
// member's delegate credentials that still act, which a change leaves alone.
type PassphraseChange struct {
	Token         string
	ExpiresAt     int64
	LifeMs        int64
	SessionsEnded int64
	Delegates     int64
}

// ChangePassphrase replaces the passphrase for the member whose session the token names, when the current
// passphrase proves it. In the transaction that stores the new one, it ends every other session of that member and
// gives the session the change came from a new token, so a copy of the old token, which a thief of the cookie holds,
// opens nothing once the passphrase changes. The session must still stand when the change commits, so a sign-out that
// beat the change undoes it.
//
// The current passphrase is a guess at the passphrase like a sign-in's, so a refusal of it is on record as a
// refused sign-in is, naming the member and no part of the guess. A change that lost to another, with a current
// passphrase that was right, is no guess and is not on record. The caller holds the one-at-a-time gate sign-in holds.
func (s *Sessions) ChangePassphrase(ctx context.Context, scope ledger.WorkspaceID, current, next ports.Proof, token string) (PassphraseChange, error) {
	actor, err := s.Member(ctx, scope, token)
	if err != nil {
		return PassphraseChange{}, err
	}
	var changed PassphraseChange
	err = s.identity.Change(ctx, scope, current, next, actor, func(tx ports.SharedTx, member string) error {
		changed = PassphraseChange{}
		now := tx.Now()
		session, err := standingSession(tx, token, member, now)
		if err != nil {
			return err
		}
		fresh, err := newSecret()
		if err != nil {
			return err
		}
		err = tx.Members().InsertSession(authority.Session{Digest: digestOf(fresh), Member: member, CreatedAt: now, ExpiresAt: session.ExpiresAt})
		if err != nil {
			return err
		}
		// Every session but the new one goes, the old token of this session with the others. The count holds the
		// sessions that had not expired, and this session's old token is one of them.
		dropped, err := tx.Members().DropOtherMemberSessions(member, digestOf(fresh), now)
		if err != nil {
			return err
		}
		changed = PassphraseChange{Token: fresh, ExpiresAt: session.ExpiresAt, LifeMs: session.ExpiresAt - now, SessionsEnded: dropped - 1}
		if changed.Delegates, err = actingDelegates(tx, member, now); err != nil {
			return err
		}
		return recordSessionsEnded(tx, actor, member, changed.SessionsEnded)
	})
	if errors.Is(err, authority.ErrBadProof) {
		// As a refused sign-in is, whether or not the client is still there.
		if recorded := s.recordChangeRefusal(context.WithoutCancel(ctx), scope, actor); recorded != nil {
			return PassphraseChange{}, errors.Join(err, recorded)
		}
	}
	if err != nil {
		return PassphraseChange{}, err
	}
	return changed, nil
}

func (s *Sessions) recordChangeRefusal(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) error {
	return s.store.Installation(scope).Write(ctx, "member.passphrase_change_refused", func(tx ports.SharedTx) error {
		return appendSignIn(tx, eventSignInRefused, actor, 0)
	})
}

// standingSession reads the session a change of the passphrase came from, in the transaction that stores the new one.
// It must still be live and the member's when the change commits, so a sign-out that beat the change undoes it.
func standingSession(tx ports.SharedTx, token, member string, now int64) (authority.Session, error) {
	session, err := tx.Members().Session(digestOf(token))
	if errors.Is(err, ports.ErrNotFound) {
		return authority.Session{}, fmt.Errorf("%w: this session ended", authority.ErrNoSession)
	}
	if err != nil {
		return authority.Session{}, err
	}
	if !session.Live(now) || session.Member != member {
		return authority.Session{}, fmt.Errorf("%w: this session is not the member's", authority.ErrNoSession)
	}
	return session, nil
}

// actingDelegates counts the member's delegate credentials that still act, which a change of the passphrase leaves alone.
func actingDelegates(tx ports.SharedTx, member string, now int64) (int64, error) {
	credentials, err := tx.Members().Delegates(member)
	if err != nil {
		return 0, err
	}
	var acting int64
	for _, credential := range credentials {
		if credential.State(now) == authority.DelegateActive {
			acting++
		}
	}
	return acting, nil
}

// recordSessionsEnded records how many of the member's other sessions a change of the passphrase ended.
func recordSessionsEnded(tx ports.SharedTx, actor ledger.Actor, member string, sessions int64) error {
	data, err := json.Marshal(struct {
		Sessions int64  `json:"sessions"`
		Reason   string `json:"reason"`
	}{sessions, "passphrase_changed"})
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{Type: EventSessionsEnded, Actor: actor, Subject: ledger.Subject{Kind: subjectMember, ID: member}, Data: data})
	return err
}

// Enrolled says whether a passphrase is set, so a first run knows whether to ask for one.
func (s *Sessions) Enrolled(ctx context.Context, scope ledger.WorkspaceID) (bool, error) {
	return s.identity.Enrolled(ctx, scope)
}

// Enroll sets a passphrase. A caller with no session counts as the first enrollment, and
// the provider refuses that once a passphrase is set. The first enrollment asks for the orchestrator of each registered
// worker that has none, in the transaction that stores the passphrase, when StartingOrchestrators is on.
//
// A later one comes from a member's session on the socket, which a terminal holds and no cookie carries, so that
// session cannot go on under a new token as it does after a change from the web UI. In the transaction that stores the
// new passphrase, a later set ends every session of the member, the one it came from included, so no copy of an old
// token opens anything once the passphrase changes, and it records the sessions it ended as a change does. The result
// counts the member's other sessions that ended and the delegate credentials that still act, and holds no token.
func (s *Sessions) Enroll(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof, token string) (PassphraseChange, error) {
	actor, err := s.Member(ctx, scope, token)
	if errors.Is(err, authority.ErrNoSession) {
		return PassphraseChange{}, s.identity.Enroll(ctx, scope, proof, firstEnrollment, s.startOrchestrators())
	}
	if err != nil {
		return PassphraseChange{}, err
	}
	var changed PassphraseChange
	err = s.identity.Enroll(ctx, scope, proof, actor, func(tx ports.SharedTx, member string) error {
		changed = PassphraseChange{}
		now := tx.Now()
		if _, err := standingSession(tx, token, member, now); err != nil {
			return err
		}
		// Every session of the member goes, this one with the others. The count holds the sessions that had not
		// expired, and this one is one of them.
		dropped, err := tx.Members().DropMemberSessions(member, now)
		if err != nil {
			return err
		}
		changed.SessionsEnded = dropped - 1
		if changed.Delegates, err = actingDelegates(tx, member, now); err != nil {
			return err
		}
		return recordSessionsEnded(tx, actor, member, changed.SessionsEnded)
	})
	if err != nil {
		return PassphraseChange{}, err
	}
	return changed, nil
}
