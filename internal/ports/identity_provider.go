package ports

import (
	"context"

	"ropeshift.dev/ropeshift/internal/ledger"
)

// Proof is what a human presents to sign in. A local provider reads a passphrase, and a
// later provider adds its own field.
type Proof struct {
	Passphrase string
}

// IdentityProvider authenticates humans (FR-AUTH-3). It answers who a proof belongs to and
// holds no session: the control plane issues those, so every provider gets the same one.
type IdentityProvider interface {
	// Enrolled reports whether anyone can sign in yet.
	Enrolled(ctx context.Context, scope ledger.WorkspaceID) (bool, error)
	// Enroll sets the proof of a workspace's member. The first needs no session, because
	// nothing yet can prove one, and each later one needs a member.
	Enroll(ctx context.Context, scope ledger.WorkspaceID, proof Proof, actor ledger.Actor) error
	// Authenticate names the member a proof belongs to, or returns authority.ErrBadProof
	// or authority.ErrNotEnrolled.
	Authenticate(ctx context.Context, scope ledger.WorkspaceID, proof Proof) (string, error)
}
