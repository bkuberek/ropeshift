package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
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
	// nothing yet can prove one, and each later one needs a member. alongside, when not nil, runs in the
	// transaction that stores the proof, told the member's ID, and its error undoes the set, as Change's does.
	Enroll(ctx context.Context, scope ledger.WorkspaceID, proof Proof, actor ledger.Actor, alongside func(tx SharedTx, member string) error) error
	// Change replaces the proof of the member that current proves with next. It is for a member who is
	// signed in, so an actor that holds no session is refused with authority.ErrNoSession, before anything
	// is checked. It then refuses authority.ErrNotEnrolled while no proof is set, authority.ErrBadProof
	// unless current proves the member, and authority.ErrInvalid for a next outside the bounds of
	// authority.CheckPassphrase, in that order, so a caller who cannot prove the current one learns nothing of
	// what a new one needs. A change replaces only the proof it verified: a proof that moved since, because
	// another change landed first, refuses with authority.ErrPassphraseChanged, which is no wrong guess. alongside, when
	// not nil, runs in the transaction that stores the new proof, told the member's ID, and its error undoes
	// the change, so what it writes stands or falls with the proof.
	Change(ctx context.Context, scope ledger.WorkspaceID, current, next Proof, actor ledger.Actor, alongside func(tx SharedTx, member string) error) error
	// Authenticate names the member a proof belongs to, or returns authority.ErrBadProof
	// or authority.ErrNotEnrolled. alongside, when not nil, then runs in a write that finds the proof it checked still
	// in force, told the member's ID, so what it writes, such as a session, cannot outlive a change that landed after
	// the check: a proof that moved since refuses with authority.ErrPassphraseChanged, which is no wrong guess, and
	// alongside does not run. Its error undoes the write.
	Authenticate(ctx context.Context, scope ledger.WorkspaceID, proof Proof, alongside func(tx SharedTx, member string) error) (string, error)
}
