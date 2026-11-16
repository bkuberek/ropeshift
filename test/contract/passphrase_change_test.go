package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/localauth"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// digestOfToken is the digest the ledger keeps of a session's token.
func digestOfToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// twoSessions enrolls a passphrase and signs in twice, as a browser and a terminal would.
func twoSessions(t *testing.T) (*Rig, *control.Sessions, control.SessionGrant, control.SessionGrant) {
	t.Helper()
	rig := NewRig(t, SQLiteFile)
	sessions := control.NewSessions(rig.Store(), localauth.New(rig.Store(), cheapHash))
	if err := sessions.Enroll(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ""); err != nil {
		t.Fatal(err)
	}
	browser, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, 0)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return rig, sessions, browser, terminal
}

func installationEvents(t *testing.T, rig *Rig) []ledger.Event {
	t.Helper()
	events, err := sharedEvents(rig, rig.Store())
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// TestAPassphraseChangeEndsTheMembersOtherSessionsAndKeepsTheOneItCameFrom holds the change's whole effect to
// one transaction: the new proof, the sessions that end, and the events that say so.
func TestAPassphraseChangeEndsTheMembersOtherSessionsAndKeepsTheOneItCameFrom(t *testing.T) {
	t.Parallel()
	rig, sessions, browser, terminal := twoSessions(t)
	ended, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, browser.Token)
	if err != nil {
		t.Fatal(err)
	}
	if ended != 1 {
		t.Errorf("the change ended %d sessions, want the terminal's alone", ended)
	}
	if _, err := sessions.Member(rig.Ctx, rig.Scope, browser.Token); err != nil {
		t.Errorf("the session the change came from ended: %v", err)
	}
	if _, err := sessions.Member(rig.Ctx, rig.Scope, terminal.Token); !errors.Is(err, authority.ErrNoSession) {
		t.Errorf("another session of the member outlived the change: %v", err)
	}
	if _, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, 0); !errors.Is(err, authority.ErrBadProof) {
		t.Errorf("the old passphrase still signs in: %v", err)
	}
	if _, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, 0); err != nil {
		t.Errorf("the new passphrase: %v", err)
	}
	var kinds []string
	for _, event := range installationEvents(t, rig) {
		kinds = append(kinds, event.Type)
		if strings.Contains(string(event.Data), goodPassphrase) || strings.Contains(string(event.Data), otherPassphrase) {
			t.Errorf("event %s holds a passphrase", event.Type)
		}
	}
	for _, want := range []string{"member.proof_set", control.EventSessionsEnded} {
		if !slices.Contains(kinds, want) {
			t.Errorf("the installation's log has no %s: %v", want, kinds)
		}
	}
}

// TestAWrongCurrentPassphraseChangesNothingAndLeavesARefusalOnRecord holds the guess a change allows to the
// price of a sign-in: it hashes, it is refused, and it is on record with no trace of what was tried.
func TestAWrongCurrentPassphraseChangesNothingAndLeavesARefusalOnRecord(t *testing.T) {
	t.Parallel()
	rig, sessions, browser, terminal := twoSessions(t)
	const guess = "a guess at the current passphrase"
	before := len(installationEvents(t, rig))
	for _, current := range []string{guess, ""} {
		ended, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: current}, ports.Proof{Passphrase: otherPassphrase}, browser.Token)
		if !errors.Is(err, authority.ErrBadProof) || ended != 0 {
			t.Fatalf("current %q: ended %d, %v", current, ended, err)
		}
	}
	for _, token := range []string{browser.Token, terminal.Token} {
		if _, err := sessions.Member(rig.Ctx, rig.Scope, token); err != nil {
			t.Errorf("a refused change ended a session: %v", err)
		}
	}
	if _, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, 0); err != nil {
		t.Errorf("a refused change moved the passphrase: %v", err)
	}
	events := installationEvents(t, rig)
	refused := 0
	for _, event := range events[before:] {
		if event.Type == "member.sign_in_refused" {
			refused++
		}
		if strings.Contains(string(event.Data), guess) {
			t.Errorf("event %s holds the guess", event.Type)
		}
	}
	if refused != 2 {
		t.Errorf("%d refusals on record, want one for each guess", refused)
	}
}

func TestAPassphraseChangeNeedsALiveSession(t *testing.T) {
	t.Parallel()
	rig, sessions, browser, terminal := twoSessions(t)
	if err := sessions.End(rig.Ctx, rig.Scope, browser.Token, ledger.Actor{Kind: ledger.ActorMember, ID: browser.Member}); err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"none": "", "unknown": "not-a-session", "one that ended": browser.Token} {
		ended, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, token)
		if !errors.Is(err, authority.ErrNoSession) || ended != 0 {
			t.Errorf("%s: ended %d, %v", name, ended, err)
		}
	}
	if _, err := sessions.Member(rig.Ctx, rig.Scope, terminal.Token); err != nil {
		t.Errorf("a refused change ended a session: %v", err)
	}
	if _, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, 0); err != nil {
		t.Errorf("a refused change moved the passphrase: %v", err)
	}
}

// TestTwoChangesFromOnePassphraseCannotBothStand holds a change to the passphrase it checked: two requests of
// one session that both read the current passphrase before either writes cannot both replace it, because the
// second finds the proof it verified gone. Each round races the pair, so a change that wrote over whatever stood
// would let both through in some round.
func TestTwoChangesFromOnePassphraseCannotBothStand(t *testing.T) {
	t.Parallel()
	rig, sessions, browser, _ := twoSessions(t)
	current := goodPassphrase
	for round := range 25 {
		var wg sync.WaitGroup
		outcomes := make([]error, 2)
		candidates := []string{current + "-a", current + "-b"}
		for i := range candidates {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, outcomes[i] = sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: current}, ports.Proof{Passphrase: candidates[i]}, browser.Token)
			}()
		}
		wg.Wait()
		winners := 0
		for i, outcome := range outcomes {
			switch {
			case outcome == nil:
				winners++
				current = candidates[i]
			case !errors.Is(outcome, authority.ErrBadProof):
				t.Fatalf("round %d: change %d was refused with %v, want a bad proof", round, i, outcome)
			}
		}
		if winners != 1 {
			t.Fatalf("round %d: %d changes from one passphrase stood: %v", round, winners, outcomes)
		}
		if _, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: current}, 0); err != nil {
			t.Fatalf("round %d: the winner's passphrase does not sign in: %v", round, err)
		}
	}
}

func TestAPassphraseChangeIsRefusedWhileNoneIsSet(t *testing.T) {
	t.Parallel()
	rig := NewRig(t, SQLiteFile)
	store := rig.Store()
	sessions := control.NewSessions(store, localauth.New(store, cheapHash))
	// A session left from before the member's record went, as a restored installation database can leave one.
	err := store.Installation(rig.Scope).Write(rig.Ctx, "test.session", func(tx ports.SharedTx) error {
		if err := tx.Members().InsertSession(authority.Session{Digest: digestOfToken("orphan"), Member: localauth.Member, CreatedAt: tx.Now(), ExpiresAt: tx.Now() + 60_000}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.session", Actor: rig.Actor, Subject: ledger.Subject{Kind: "member", ID: localauth.Member}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ended, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, "orphan")
	if !errors.Is(err, authority.ErrNotEnrolled) || ended != 0 {
		t.Fatalf("ended %d, %v", ended, err)
	}
	if enrolled, err := sessions.Enrolled(rig.Ctx, rig.Scope); err != nil || enrolled {
		t.Fatalf("a refused change set the first passphrase: enrolled %v, %v", enrolled, err)
	}
}
