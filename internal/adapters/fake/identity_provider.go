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
}

// NewIdentityProvider returns a provider nobody has enrolled with.
func NewIdentityProvider() *IdentityProvider {
	return &IdentityProvider{passphrases: map[ledger.WorkspaceID]string{}}
}

// Within gives the provider the store whose installation records a change writes beside the passphrase, and
// returns the provider. A real provider keeps its proof in those records, so a change's companion write shares a
// transaction with it; this one keeps its passphrase in memory and swaps it once that transaction has committed.
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

// Enroll sets the passphrase.
func (p *IdentityProvider) Enroll(_ context.Context, scope ledger.WorkspaceID, proof ports.Proof, actor ledger.Actor) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := authority.CheckPassphrase(proof.Passphrase); err != nil {
		return err
	}
	if _, ok := p.passphrases[scope]; ok && !authority.Allowed(actor, authority.ActionSetPassphrase) {
		return authority.ErrNoSession
	}
	p.passphrases[scope] = proof.Passphrase
	return nil
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
	if alongside != nil {
		if p.store == nil {
			return errors.New("the fake identity provider has no store for a change's companion write: give it one with Within")
		}
		err := p.store.Installation(scope).Write(ctx, "member.change_proof", func(tx ports.SharedTx) error { return alongside(tx, Member) })
		if err != nil {
			return err
		}
	}
	p.passphrases[scope] = next.Passphrase
	return nil
}

// Authenticate names the member a passphrase belongs to.
func (p *IdentityProvider) Authenticate(_ context.Context, scope ledger.WorkspaceID, proof ports.Proof) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	want, ok := p.passphrases[scope]
	if !ok {
		return "", authority.ErrNotEnrolled
	}
	if proof.Passphrase != want {
		return "", authority.ErrBadProof
	}
	return Member, nil
}

var _ ports.IdentityProvider = (*IdentityProvider)(nil)
