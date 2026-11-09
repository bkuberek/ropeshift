package fake

import (
	"context"
	"fmt"
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
}

// NewIdentityProvider returns a provider nobody has enrolled with.
func NewIdentityProvider() *IdentityProvider {
	return &IdentityProvider{passphrases: map[ledger.WorkspaceID]string{}}
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
	if len(proof.Passphrase) < authority.MinPassphrase {
		return fmt.Errorf("%w: a passphrase has at least %d bytes", authority.ErrInvalid, authority.MinPassphrase)
	}
	if _, ok := p.passphrases[scope]; ok && !authority.Allowed(actor, authority.ActionSetPassphrase) {
		return authority.ErrNoSession
	}
	p.passphrases[scope] = proof.Passphrase
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
