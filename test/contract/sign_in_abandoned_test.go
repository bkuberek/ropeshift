package contract

import (
	"context"
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/localauth"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// abandoning is an identity provider whose client goes away the moment its check is done, which is when a
// client that means to guess without being counted goes. The check ran, and what it found is what the provider
// returns.
type abandoning struct {
	ports.IdentityProvider
	cancel context.CancelFunc
}

func (a abandoning) Authenticate(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof) (string, error) {
	member, err := a.IdentityProvider.Authenticate(ctx, scope, proof)
	a.cancel()
	return member, err
}

func (a abandoning) Change(ctx context.Context, scope ledger.WorkspaceID, current, next ports.Proof, actor ledger.Actor, alongside func(tx ports.SharedTx, member string) error) error {
	err := a.IdentityProvider.Change(ctx, scope, current, next, actor, alongside)
	a.cancel()
	return err
}

// A wrong passphrase is a wrong passphrase when the client that sent it has gone, so it is on record and it comes
// back as one. A refusal that failed to be written for want of a client would come back as that failure, and
// whatever counts wrong guesses would take a wrong guess for something that proves nothing.
func TestAWrongPassphraseWhoseClientWentAwayAfterTheCheckIsOnRecordAndComesBackAsWrong(t *testing.T) {
	t.Parallel()
	rig, _, browser, _ := twoSessions(t)
	identity := localauth.New(rig.Store(), cheapHash)
	before := refusals(installationEvents(t, rig))

	signIn, cancelSignIn := context.WithCancel(rig.Ctx)
	defer cancelSignIn()
	_, err := control.NewSessions(rig.Store(), abandoning{identity, cancelSignIn}).SignIn(signIn, rig.Scope, ports.Proof{Passphrase: "a passphrase nobody chose"}, 0)
	if !errors.Is(err, authority.ErrBadProof) {
		t.Fatalf("a wrong passphrase whose client went away answers %v", err)
	}
	if got := refusals(installationEvents(t, rig)); got != before+1 {
		t.Errorf("the refusal of the sign-in is on record %d times, want %d", got-before, 1)
	}

	change, cancelChange := context.WithCancel(rig.Ctx)
	defer cancelChange()
	_, err = control.NewSessions(rig.Store(), abandoning{identity, cancelChange}).ChangePassphrase(change, rig.Scope,
		ports.Proof{Passphrase: "a passphrase nobody chose"}, ports.Proof{Passphrase: "a passphrase for the next season"}, browser.Token)
	if !errors.Is(err, authority.ErrBadProof) {
		t.Fatalf("a wrong current passphrase whose client went away answers %v", err)
	}
	if got := refusals(installationEvents(t, rig)); got != before+2 {
		t.Errorf("the refusal of the change is on record %d times, want %d", got-before-1, 1)
	}
}
