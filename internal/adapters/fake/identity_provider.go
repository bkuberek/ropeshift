package fake

import (
	"context"
	"errors"
	"sync"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Member is the one member the fake provider signs in.
const Member = "member-1"

// IdentityProvider keeps one passphrase per workspace in memory.
type IdentityProvider struct {
	mu          sync.Mutex
	passphrases map[ledger.WorkspaceID]string
	store       ports.Store
	// sets counts the passphrases set in each workspace, as a real provider's stored hash, salted anew each time,
	// tells one set from the next, so a sign-in's write can find whether the passphrase it checked still stands. It
	// has a lock of its own, under which nothing else is locked, because a sign-in reads it inside a transaction while
	// a set holds mu waiting for one.
	setsMu sync.Mutex
	sets   map[ledger.WorkspaceID]int
}

// NewIdentityProvider returns a provider nobody has enrolled with.
func NewIdentityProvider() *IdentityProvider {
	return &IdentityProvider{passphrases: map[ledger.WorkspaceID]string{}, sets: map[ledger.WorkspaceID]int{}}
}

func (p *IdentityProvider) setsIn(scope ledger.WorkspaceID) int {
	p.setsMu.Lock()
	defer p.setsMu.Unlock()
	return p.sets[scope]
}

func (p *IdentityProvider) count(scope ledger.WorkspaceID) {
	p.setsMu.Lock()
	defer p.setsMu.Unlock()
	p.sets[scope]++
}

// set stores a passphrase. A companion runs first, in the write accompany makes, and the set is counted inside that
// write, so a sign-in whose write the store orders after it finds the passphrase it checked gone; the passphrase is
// swapped once the write has committed. With no companion, nothing is written, and the set is counted as it is made.
func (p *IdentityProvider) set(ctx context.Context, scope ledger.WorkspaceID, op, passphrase string, alongside func(ports.SharedTx, string) error) error {
	if alongside == nil {
		p.passphrases[scope] = passphrase
		p.count(scope)
		return nil
	}
	err := p.accompany(ctx, scope, op, func(tx ports.SharedTx, member string) error {
		if err := alongside(tx, member); err != nil {
			return err
		}
		p.count(scope)
		return nil
	})
	if err != nil {
		return err
	}
	p.passphrases[scope] = passphrase
	return nil
}

// Within gives the provider the store whose installation records a set, a change, or a sign-in writes beside the
// passphrase, and returns the provider. A real provider keeps its proof in those records, so a companion write shares
// a transaction with it; this one keeps its passphrase in memory and swaps it once that transaction has committed.
func (p *IdentityProvider) Within(store ports.Store) *IdentityProvider {
	p.store = store
	return p
}

// Enrolled reports whether a passphrase is set.
func (p *IdentityProvider) Enrolled(_ context.Context, scope ledger.WorkspaceID) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.passphrases[scope]
	return ok, nil
}

// Enroll sets the passphrase. The companion's transaction commits before the swap, as a change's does.
func (p *IdentityProvider) Enroll(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof, actor ledger.Actor, alongside func(ports.SharedTx, string) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := authority.CheckPassphrase(proof.Passphrase); err != nil {
		return err
	}
	if _, ok := p.passphrases[scope]; ok && !authority.Allowed(actor, authority.ActionSetPassphrase) {
		return authority.ErrNoSession
	}
	return p.set(ctx, scope, "member.enroll", proof.Passphrase, alongside)
}

// accompany runs a companion write in a transaction of the store's installation records, which a real provider's
// proof shares, under the operation a real provider names.
func (p *IdentityProvider) accompany(ctx context.Context, scope ledger.WorkspaceID, op string, alongside func(ports.SharedTx, string) error) error {
	if alongside == nil {
		return nil
	}
	if p.store == nil {
		return errors.New("the fake identity provider has no store for a companion write: give it one with Within")
	}
	return p.store.Installation(scope).Write(ctx, op, func(tx ports.SharedTx) error { return alongside(tx, Member) })
}

// Change replaces the passphrase when the current one is right. The lock is held from the check to the swap, so
// two changes cannot both replace what they checked, and the companion's transaction commits before the swap.
func (p *IdentityProvider) Change(ctx context.Context, scope ledger.WorkspaceID, current, next ports.Proof, actor ledger.Actor, alongside func(ports.SharedTx, string) error) error {
	if !authority.Allowed(actor, authority.ActionSetPassphrase) {
		return authority.ErrNoSession
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	want, ok := p.passphrases[scope]
	if !ok {
		return authority.ErrNotEnrolled
	}
	if current.Passphrase != want {
		return authority.ErrBadProof
	}
	if err := authority.CheckPassphrase(next.Passphrase); err != nil {
		return err
	}
	return p.set(ctx, scope, "member.change_proof", next.Passphrase, alongside)
}

// Authenticate names the member a passphrase belongs to. A companion then runs in a write that finds no set counted
// since the check, as a real provider's finds the hash it checked, and the lock is not held across that write, so a
// change can land between the check and it.
func (p *IdentityProvider) Authenticate(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof, alongside func(ports.SharedTx, string) error) (string, error) {
	p.mu.Lock()
	want, ok := p.passphrases[scope]
	checked := p.setsIn(scope)
	p.mu.Unlock()
	if !ok {
		return "", authority.ErrNotEnrolled
	}
	if proof.Passphrase != want {
		return "", authority.ErrBadProof
	}
	if alongside == nil {
		return Member, nil
	}
	err := p.accompany(ctx, scope, "member.sign_in", func(tx ports.SharedTx, member string) error {
		if p.setsIn(scope) != checked {
			return authority.ErrPassphraseChanged
		}
		return alongside(tx, member)
	})
	if err != nil {
		return "", err
	}
	return Member, nil
}

var _ ports.IdentityProvider = (*IdentityProvider)(nil)
