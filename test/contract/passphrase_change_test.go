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
	if _, err := sessions.Enroll(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ""); err != nil {
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

func refusals(events []ledger.Event) int {
	count := 0
	for _, event := range events {
		if event.Type == "member.sign_in_refused" {
			count++
		}
	}
	return count
}

// sessionDigests lists the digests of the sessions the ledger holds.
func sessionDigests(t *testing.T, rig *Rig) []string {
	t.Helper()
	var digests []string
	err := rig.Store().Installation(rig.Scope).Read(rig.Ctx, func(tx ports.SharedReadTx) error {
		sessions, err := tx.Members().Sessions()
		for _, session := range sessions {
			digests = append(digests, session.Digest)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return digests
}

// TestAPassphraseChangeEndsTheMembersOtherSessionsAndRotatesTheOneItCameFrom holds the change's whole effect to
// one transaction: the new proof, the sessions that end, the new token that replaces the caller's, and the events
// that say so. The caller stays signed in, on a token a copy of the old cookie does not open.
func TestAPassphraseChangeEndsTheMembersOtherSessionsAndRotatesTheOneItCameFrom(t *testing.T) {
	t.Parallel()
	rig, sessions, browser, terminal := twoSessions(t)
	changed, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, browser.Token)
	if err != nil {
		t.Fatal(err)
	}
	if changed.SessionsEnded != 1 {
		t.Errorf("the change ended %d other sessions, want the terminal's alone", changed.SessionsEnded)
	}
	if changed.Token == "" || changed.Token == browser.Token {
		t.Fatalf("the session the change came from was not given a new token: %q", changed.Token)
	}
	if changed.ExpiresAt != browser.ExpiresAt {
		t.Errorf("the new session ends at %d, where the old one did at %d: a change must not lengthen a session", changed.ExpiresAt, browser.ExpiresAt)
	}
	if changed.LifeMs <= 0 || changed.LifeMs > authority.LoginTTLMs {
		t.Errorf("the new session has %d ms left", changed.LifeMs)
	}
	if _, err := sessions.Member(rig.Ctx, rig.Scope, changed.Token); err != nil {
		t.Errorf("the new token opens nothing: %v", err)
	}
	if _, err := sessions.Member(rig.Ctx, rig.Scope, browser.Token); !errors.Is(err, authority.ErrNoSession) {
		t.Errorf("the old token of the session the change came from still opens: %v", err)
	}
	if _, err := sessions.Member(rig.Ctx, rig.Scope, terminal.Token); !errors.Is(err, authority.ErrNoSession) {
		t.Errorf("another session of the member outlived the change: %v", err)
	}
	if left := sessionDigests(t, rig); len(left) != 1 || left[0] != digestOfToken(changed.Token) {
		t.Errorf("the sessions left are %v, want the new one alone", left)
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
		for _, secret := range []string{goodPassphrase, otherPassphrase, browser.Token, changed.Token} {
			if strings.Contains(string(event.Data), secret) {
				t.Errorf("event %s holds a secret", event.Type)
			}
		}
	}
	for _, want := range []string{"member.proof_set", control.EventSessionsEnded} {
		if !slices.Contains(kinds, want) {
			t.Errorf("the installation's log has no %s: %v", want, kinds)
		}
	}
}

// A session that expired and that no sweep has removed yet is removed by the change and is not counted as ended:
// the count says what the change took from the member, and an expired session was not theirs to lose.
func TestAPassphraseChangeCountsOnlyTheSessionsThatHadNotExpired(t *testing.T) {
	t.Parallel()
	rig, sessions, browser, _ := twoSessions(t)
	expiredSession(t, rig)
	changed, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, browser.Token)
	if err != nil {
		t.Fatal(err)
	}
	if changed.SessionsEnded != 1 {
		t.Errorf("the change ended %d other sessions, want the terminal's alone", changed.SessionsEnded)
	}
	if left := sessionDigests(t, rig); len(left) != 1 || left[0] != digestOfToken(changed.Token) {
		t.Errorf("the sessions left are %v, want the new one alone", left)
	}
}

// expiredSession leaves a session of the member that expired and that no sweep has removed yet.
func expiredSession(t *testing.T, rig *Rig) {
	t.Helper()
	err := rig.Store().Installation(rig.Scope).Write(rig.Ctx, "test.expired_session", func(tx ports.SharedTx) error {
		members, err := tx.Members().Sessions()
		if err != nil {
			return err
		}
		if err := tx.Members().InsertSession(authority.Session{Digest: "expired", Member: members[0].Member, CreatedAt: tx.Now() - 120_000, ExpiresAt: tx.Now() - 60_000}); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: "test.expired_session", Actor: rig.Actor, Subject: ledger.Subject{Kind: "member", ID: members[0].Member}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A later set of the passphrase from a member's session, as `ropeshift passphrase set` makes one on the socket, ends
// every session of the member in the transaction that stores it. A terminal holds no cookie a new token could reach,
// so the session the set came from ends with the others. The count holds the others that had not expired, as a
// change's does, and the log records it.
func TestALaterSetEndsEverySessionOfTheMember(t *testing.T) {
	t.Parallel()
	rig, sessions, browser, terminal := twoSessions(t)
	expiredSession(t, rig)
	changed, err := sessions.Enroll(rig.Ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, terminal.Token)
	if err != nil {
		t.Fatal(err)
	}
	if changed.SessionsEnded != 1 || changed.Delegates != 0 || changed.Token != "" {
		t.Errorf("the set says %+v, want the browser's session ended, no credential acting, and no token", changed)
	}
	for name, token := range map[string]string{"the terminal's": terminal.Token, "the browser's": browser.Token} {
		if _, err := sessions.Member(rig.Ctx, rig.Scope, token); !errors.Is(err, authority.ErrNoSession) {
			t.Errorf("%s session outlived the set: %v", name, err)
		}
	}
	if left := sessionDigests(t, rig); len(left) != 0 {
		t.Errorf("the sessions left are %v, want none", left)
	}
	if _, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, 0); err != nil {
		t.Errorf("the new passphrase: %v", err)
	}
	var ended []string
	for _, event := range installationEvents(t, rig) {
		if event.Type == control.EventSessionsEnded {
			ended = append(ended, string(event.Data))
		}
	}
	if len(ended) != 1 || !strings.Contains(ended[0], `"sessions":1`) {
		t.Errorf("the log records %v, want one %s that counts the browser's session", ended, control.EventSessionsEnded)
	}
}

// A later set needs its session to stand when it commits, as a change does, so a sign-out that beat it undoes it.
func TestALaterSetFromASessionThatEndedSetsNothing(t *testing.T) {
	t.Parallel()
	rig, sessions, _, terminal := twoSessions(t)
	if err := sessions.End(rig.Ctx, rig.Scope, terminal.Token, ledger.Actor{Kind: ledger.ActorMember, ID: terminal.Member}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Enroll(rig.Ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, terminal.Token); !errors.Is(err, authority.ErrNoSession) {
		t.Fatalf("got %v, want ErrNoSession", err)
	}
	if _, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, 0); err != nil {
		t.Errorf("a refused set moved the passphrase: %v", err)
	}
}

// A change ends sessions and no credential: a delegate's credential keeps acting, as the documents say, and the
// answer counts the credentials that do, so the page can offer the sign-out that ends them.
func TestAPassphraseChangeLeavesTheMembersDelegateCredentialsAndCountsThem(t *testing.T) {
	t.Parallel()
	rig, sessions, browser, _ := twoSessions(t)
	member := ledger.Actor{Kind: ledger.ActorMember, ID: localauth.Member}
	delegates := control.NewDelegates(rig.Store())
	live, err := delegates.Create(rig.Ctx, rig.Scope, authority.DelegateRequest{Member: member.ID, Project: rig.Scope, Label: "ci", ExpiresInMs: authority.MaxDelegateTTLMs}, member)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := delegates.Create(rig.Ctx, rig.Scope, authority.DelegateRequest{Member: member.ID, Project: rig.Scope, Label: "old", ExpiresInMs: authority.MaxDelegateTTLMs}, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := delegates.Revoke(rig.Ctx, rig.Scope, revoked.Credential.ID, member); err != nil {
		t.Fatal(err)
	}
	changed, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, browser.Token)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Delegates != 1 {
		t.Errorf("the change counts %d credentials that act, want the one that does", changed.Delegates)
	}
	err = rig.Store().Installation(rig.Scope).Read(rig.Ctx, func(tx ports.SharedReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		credential, err := tx.Members().Delegate(live.Credential.ID)
		if err != nil {
			return err
		}
		if credential.State(now) != authority.DelegateActive {
			t.Errorf("the change ended a delegate credential: %s", credential.State(now))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
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
		changed, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: current}, ports.Proof{Passphrase: otherPassphrase}, browser.Token)
		if !errors.Is(err, authority.ErrBadProof) || changed.Token != "" {
			t.Fatalf("current %q: %+v, %v", current, changed, err)
		}
	}
	for _, token := range []string{browser.Token, terminal.Token} {
		if _, err := sessions.Member(rig.Ctx, rig.Scope, token); err != nil {
			t.Errorf("a refused change ended or moved a session: %v", err)
		}
	}
	if _, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, 0); err != nil {
		t.Errorf("a refused change moved the passphrase: %v", err)
	}
	events := installationEvents(t, rig)
	if got := refusals(events[before:]); got != 2 {
		t.Errorf("%d refusals on record, want one for each guess", got)
	}
	for _, event := range events[before:] {
		if strings.Contains(string(event.Data), guess) {
			t.Errorf("event %s holds the guess", event.Type)
		}
	}
}

func TestAPassphraseChangeNeedsALiveSession(t *testing.T) {
	t.Parallel()
	rig, sessions, browser, terminal := twoSessions(t)
	if err := sessions.End(rig.Ctx, rig.Scope, browser.Token, ledger.Actor{Kind: ledger.ActorMember, ID: browser.Member}); err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"none": "", "unknown": "not-a-session", "one that ended": browser.Token} {
		changed, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, token)
		if !errors.Is(err, authority.ErrNoSession) || changed.Token != "" {
			t.Errorf("%s: %+v, %v", name, changed, err)
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
// would let both through in some round. The loser is told the passphrase moved, or that its session did, or that its
// current passphrase is wrong, according to when it began, and never that it won.
func TestTwoChangesFromOnePassphraseCannotBothStand(t *testing.T) {
	t.Parallel()
	rig, sessions, browser, _ := twoSessions(t)
	current, token := goodPassphrase, browser.Token
	for round := range 25 {
		var wg sync.WaitGroup
		outcomes := make([]control.PassphraseChange, 2)
		failures := make([]error, 2)
		candidates := []string{current + "-a", current + "-b"}
		for i := range candidates {
			wg.Add(1)
			go func() {
				defer wg.Done()
				outcomes[i], failures[i] = sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: current}, ports.Proof{Passphrase: candidates[i]}, token)
			}()
		}
		wg.Wait()
		winners := 0
		for i, failure := range failures {
			switch {
			case failure == nil:
				winners++
				current, token = candidates[i], outcomes[i].Token
			case !errors.Is(failure, authority.ErrPassphraseChanged) && !errors.Is(failure, authority.ErrBadProof) && !errors.Is(failure, authority.ErrNoSession):
				t.Fatalf("round %d: change %d was refused with %v", round, i, failure)
			}
		}
		if winners != 1 {
			t.Fatalf("round %d: %d changes from one passphrase stood: %v", round, winners, failures)
		}
		if _, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: current}, 0); err != nil {
			t.Fatalf("round %d: the winner's passphrase does not sign in: %v", round, err)
		}
	}
}

// A change whose current passphrase was right, and which another change then moved, is told so, and leaves no
// refused sign-in on record: the guess was not wrong, and a refusal on record would read as an attack.
func TestAChangeThatLostARaceIsToldSoAndLeavesNoRefusalOnRecord(t *testing.T) {
	t.Parallel()
	rig, winner, browser, _ := twoSessions(t)
	armed := true
	store := &Interposed{Store: rig.Store(), Before: func(op string) {
		if op != "member.change_proof" || !armed {
			return
		}
		armed = false
		if _, err := winner.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: thirdPassphrase}, browser.Token); err != nil {
			t.Errorf("the change that lands first: %v", err)
		}
	}}
	loser := control.NewSessions(store, localauth.New(store, cheapHash))
	before := refusals(installationEvents(t, rig))
	changed, err := loser.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, browser.Token)
	if !errors.Is(err, authority.ErrPassphraseChanged) || changed.Token != "" {
		t.Fatalf("%+v, %v", changed, err)
	}
	if errors.Is(err, authority.ErrBadProof) {
		t.Error("a lost race reads as a wrong passphrase")
	}
	if got := refusals(installationEvents(t, rig)); got != before {
		t.Errorf("a lost race left %d refused sign-ins on record", got-before)
	}
	if _, err := winner.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: thirdPassphrase}, 0); err != nil {
		t.Errorf("the passphrase that landed first: %v", err)
	}
	if _, err := winner.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, 0); !errors.Is(err, authority.ErrBadProof) {
		t.Errorf("the passphrase that lost: %v", err)
	}
}

// failingAppend is a transaction whose append of one type of event fails, which is how a check fails the last step of a
// companion, after the new hash and the end of the sessions are written in the same transaction.
type failingAppend struct {
	ports.SharedTx
	eventType string
}

var errAppendFailed = errors.New("the append failed")

func (tx failingAppend) Append(draft ledger.EventDraft) (ledger.Event, error) {
	if draft.Type == tx.eventType {
		return ledger.Event{}, errAppendFailed
	}
	return tx.SharedTx.Append(draft)
}

// newPassphraseFrom is a later set or a change of the passphrase, made from the caller's session, to otherPassphrase.
type newPassphraseFrom struct {
	name string
	// op is the write that stores the new hash.
	op  string
	run func(sessions *control.Sessions, rig *Rig, caller control.SessionGrant) error
}

var newPassphrases = []newPassphraseFrom{
	{"a later set", "member.enroll", func(sessions *control.Sessions, rig *Rig, caller control.SessionGrant) error {
		_, err := sessions.Enroll(rig.Ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, caller.Token)
		return err
	}},
	{"a change", "member.change_proof", func(sessions *control.Sessions, rig *Rig, caller control.SessionGrant) error {
		_, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, caller.Token)
		return err
	}},
}

// onlyTheOldPassphraseSignsIn holds that the passphrase in force is still goodPassphrase.
func onlyTheOldPassphraseSignsIn(t *testing.T, rig *Rig, sessions *control.Sessions) {
	t.Helper()
	if _, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, 0); !errors.Is(err, authority.ErrBadProof) {
		t.Errorf("the new passphrase stands after its set was undone: %v", err)
	}
	if _, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, 0); err != nil {
		t.Errorf("the old passphrase no longer signs in: %v", err)
	}
}

// A set or a change of the passphrase ends the member's sessions in the transaction that stores the new hash. A failure
// at the last step of ending them, after the hash and the dropped sessions are written, undoes the whole: the old
// passphrase stays in force, every session stands, and nothing is on record. Were the sessions ended in a write of
// their own after the hash committed, the new passphrase would stand beside sessions it did not end.
func TestASetOrChangeWhoseSessionEndingFailsChangesNothing(t *testing.T) {
	t.Parallel()
	for _, set := range newPassphrases {
		t.Run(set.name, func(t *testing.T) {
			t.Parallel()
			rig, plain, browser, terminal := twoSessions(t)
			store := &Interposed{Store: rig.Store(), Within: func(tx ports.SharedTx) ports.SharedTx {
				return failingAppend{SharedTx: tx, eventType: control.EventSessionsEnded}
			}}
			failing := control.NewSessions(store, localauth.New(store, cheapHash))
			before := len(installationEvents(t, rig))
			if err := set.run(failing, rig, terminal); !errors.Is(err, errAppendFailed) {
				t.Fatalf("got %v, want the failed append", err)
			}
			for who, token := range map[string]string{"the caller's": terminal.Token, "the other": browser.Token} {
				if _, err := plain.Member(rig.Ctx, rig.Scope, token); err != nil {
					t.Errorf("%s session ended though the set was undone: %v", who, err)
				}
			}
			for _, event := range installationEvents(t, rig)[before:] {
				t.Errorf("the undone set left %s on record", event.Type)
			}
			onlyTheOldPassphraseSignsIn(t, rig, plain)
		})
	}
}

// A set or a change of the passphrase reads the session it came from again in the transaction that stores the new
// hash, so a sign-out that lands between the first check of the session and that transaction undoes it: a session that
// ended cannot set the passphrase, and the new hash, the sessions it would end, and its record fall with it.
func TestASignOutThatLandsBeforeASetOrChangeWritesUndoesIt(t *testing.T) {
	t.Parallel()
	for _, set := range newPassphrases {
		t.Run(set.name, func(t *testing.T) {
			t.Parallel()
			rig, plain, browser, terminal := twoSessions(t)
			armed := true
			store := &Interposed{Store: rig.Store(), Before: func(op string) {
				if op != set.op || !armed {
					return
				}
				armed = false
				if err := plain.End(rig.Ctx, rig.Scope, terminal.Token, ledger.Actor{Kind: ledger.ActorMember, ID: terminal.Member}); err != nil {
					t.Errorf("the sign-out that lands first: %v", err)
				}
			}}
			racing := control.NewSessions(store, localauth.New(store, cheapHash))
			before := len(installationEvents(t, rig))
			err := set.run(racing, rig, terminal)
			if armed {
				t.Fatal("the set made no write for the sign-out to land before")
			}
			if !errors.Is(err, authority.ErrNoSession) {
				t.Fatalf("got %v, want ErrNoSession", err)
			}
			if _, err := plain.Member(rig.Ctx, rig.Scope, browser.Token); err != nil {
				t.Errorf("the other session ended though the set was undone: %v", err)
			}
			for _, event := range installationEvents(t, rig)[before:] {
				if event.Type != "member.signed_out" {
					t.Errorf("the undone set left %s on record", event.Type)
				}
			}
			onlyTheOldPassphraseSignsIn(t, rig, plain)
		})
	}
}

// A sign-in checks the passphrase outside any transaction, so a change can land between that check and the write that
// issues the session. The passphrase the sign-in proved is then no longer in force, and a session issued on it would
// outlive a change that ended every other one, so the sign-in issues none. The passphrase was right when it was
// checked, so the refusal says the passphrase changed, and no refused sign-in is on record.
func TestASignInThatAChangeOvertookIssuesNoSession(t *testing.T) {
	t.Parallel()
	rig, winner, browser, _ := twoSessions(t)
	armed := true
	store := &Interposed{Store: rig.Store(), Before: func(op string) {
		if op != "member.sign_in" || !armed {
			return
		}
		armed = false
		if _, err := winner.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, browser.Token); err != nil {
			t.Errorf("the change that lands first: %v", err)
		}
	}}
	late := control.NewSessions(store, localauth.New(store, cheapHash))
	before := len(installationEvents(t, rig))
	grant, err := late.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, 0)
	if armed {
		t.Fatal("the sign-in made no write for the change to land before")
	}
	if !errors.Is(err, authority.ErrPassphraseChanged) || grant.Token != "" {
		t.Fatalf("got %+v, %v; want ErrPassphraseChanged and no session", grant, err)
	}
	// The change left the session it came from, under its new token, and the sign-in added none.
	if left := sessionDigests(t, rig); len(left) != 1 {
		t.Errorf("%d sessions stand after the change, want the changer's alone", len(left))
	}
	for _, event := range installationEvents(t, rig)[before:] {
		if event.Type == control.EventSignedIn || event.Type == "member.sign_in_refused" {
			t.Errorf("the overtaken sign-in left %s on record", event.Type)
		}
	}
	if _, err := winner.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, 0); err != nil {
		t.Errorf("the new passphrase: %v", err)
	}
}

// A passphrase has an upper bound as well as a lower, because a terminal reads a line of a bounded length and a
// longer one the member could not type again, which would lock the owner out. The first set holds to it as a change
// does.
func TestAPassphraseOverTheUpperBoundIsRefusedAtTheFirstSetAndAtAChange(t *testing.T) {
	t.Parallel()
	rig := NewRig(t, SQLiteFile)
	sessions := control.NewSessions(rig.Store(), localauth.New(rig.Store(), cheapHash))
	tooLong := strings.Repeat("a", authority.MaxPassphrase+1)
	longest := strings.Repeat("b", authority.MaxPassphrase)
	if _, err := sessions.Enroll(rig.Ctx, rig.Scope, ports.Proof{Passphrase: tooLong}, ""); !errors.Is(err, authority.ErrInvalid) || !strings.Contains(err.Error(), "1024") {
		t.Fatalf("the first set of %d bytes: %v", len(tooLong), err)
	}
	if enrolled, err := sessions.Enrolled(rig.Ctx, rig.Scope); err != nil || enrolled {
		t.Fatalf("a refused first set set one: %v, %v", enrolled, err)
	}
	if _, err := sessions.Enroll(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ""); err != nil {
		t.Fatal(err)
	}
	grant, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A later set from a session is held to it too, and a wrong current passphrase is still the first thing said.
	if _, err := sessions.Enroll(rig.Ctx, rig.Scope, ports.Proof{Passphrase: tooLong}, grant.Token); !errors.Is(err, authority.ErrInvalid) {
		t.Fatalf("a later set of %d bytes: %v", len(tooLong), err)
	}
	if _, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: "a guess at the passphrase"}, ports.Proof{Passphrase: tooLong}, grant.Token); !errors.Is(err, authority.ErrBadProof) {
		t.Fatalf("a wrong current passphrase with a long new one: %v", err)
	}
	if _, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: tooLong}, grant.Token); !errors.Is(err, authority.ErrInvalid) {
		t.Fatalf("a change to %d bytes: %v", len(tooLong), err)
	}
	if _, err := sessions.SignIn(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, 0); err != nil {
		t.Fatalf("a refused change moved the passphrase: %v", err)
	}
	if _, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: longest}, grant.Token); err != nil {
		t.Fatalf("a change to %d bytes, the longest allowed: %v", len(longest), err)
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
	changed, err := sessions.ChangePassphrase(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, "orphan")
	if !errors.Is(err, authority.ErrNotEnrolled) || changed.Token != "" {
		t.Fatalf("%+v, %v", changed, err)
	}
	if enrolled, err := sessions.Enrolled(rig.Ctx, rig.Scope); err != nil || enrolled {
		t.Fatalf("a refused change set the first passphrase: enrolled %v, %v", enrolled, err)
	}
}
