package contract

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// IdentityRig is one provider and who calls it.
type IdentityRig struct {
	Provider ports.IdentityProvider
	Scope    ledger.WorkspaceID
	// Store is the store the provider writes through, which holds what a companion writes beside a proof. A check
	// reads it back, and steps into the provider's writes through it.
	Store *Interposed
	// Member is a caller with a session.
	Member ledger.Actor
	// Anyone is a caller with no session.
	Anyone ledger.Actor
}

const (
	goodPassphrase  = "a passphrase only a person knows"
	otherPassphrase = "a different passphrase entirely"
	thirdPassphrase = "a third passphrase, newer still"
)

// companionEvent is what a change's companion writes, so a check can see whether it stood or fell with the change.
const companionEvent = "test.companion"

// RunIdentityProvider runs one contract on one provider. The adapter and its fake both
// pass it, so a fake cannot drift from the provider it stands for.
func RunIdentityProvider(t *testing.T, fresh func(*testing.T) IdentityRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, IdentityRig)
	}{
		{"nobody can sign in before a passphrase is set", checkIdentityUnenrolled},
		{"the passphrase names its member", checkIdentityAuthenticates},
		{"a wrong passphrase proves nothing", checkIdentityRefusesAWrongProof},
		{"a short passphrase is refused", checkIdentityRefusesAShortProof},
		{"a second passphrase needs a member, and its companion write lands in the same transaction", checkIdentitySecondEnrollment},
		{"a second passphrase whose companion fails changes nothing", checkIdentitySecondEnrollmentIsAtomic},
		{"a change needs the current passphrase, and a wrong or missing one changes nothing", checkIdentityChangeNeedsTheCurrent},
		{"a change before a passphrase is set is refused", checkIdentityChangeUnenrolled},
		{"a change needs a member", checkIdentityChangeNeedsAMember},
		{"a change to a short passphrase is refused and changes nothing", checkIdentityChangeRefusesAShortProof},
		{"a change replaces the passphrase, and its companion write lands in the same transaction", checkIdentityChangeCompanion},
		{"a change whose companion fails changes nothing", checkIdentityChangeIsAtomic},
		{"a passphrase over the upper bound is refused at the first set and at a change, and one at the bound is not", checkIdentityRefusesATooLongProof},
		{"a sign-in's companion write lands in a write of its own, told the member", checkIdentitySignInCompanion},
		{"a sign-in whose passphrase a change replaced after the check runs no companion and is told the passphrase changed", checkIdentitySignInOvertakenByAChange},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) { check.run(t, t.Context(), fresh(t)) })
	}
}

func checkIdentityUnenrolled(t *testing.T, ctx context.Context, rig IdentityRig) {
	enrolled, err := rig.Provider.Enrolled(ctx, rig.Scope)
	if err != nil || enrolled {
		t.Fatalf("enrolled %v, %v", enrolled, err)
	}
	if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, nil); !errors.Is(err, authority.ErrNotEnrolled) {
		t.Fatalf("got %v, want ErrNotEnrolled", err)
	}
}

func enroll(t *testing.T, ctx context.Context, rig IdentityRig) {
	t.Helper()
	if err := rig.Provider.Enroll(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, rig.Anyone, nil); err != nil {
		t.Fatal(err)
	}
}

func checkIdentityAuthenticates(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	enrolled, err := rig.Provider.Enrolled(ctx, rig.Scope)
	if err != nil || !enrolled {
		t.Fatalf("enrolled %v, %v", enrolled, err)
	}
	member, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, nil)
	if err != nil || member == "" {
		t.Fatalf("member %q, %v", member, err)
	}
}

func checkIdentityRefusesAWrongProof(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	for _, wrong := range []string{"", otherPassphrase, goodPassphrase + " "} {
		if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: wrong}, nil); !errors.Is(err, authority.ErrBadProof) {
			t.Errorf("%q: got %v, want ErrBadProof", wrong, err)
		}
	}
}

func checkIdentityRefusesAShortProof(t *testing.T, ctx context.Context, rig IdentityRig) {
	err := rig.Provider.Enroll(ctx, rig.Scope, ports.Proof{Passphrase: "short"}, rig.Anyone, nil)
	if !errors.Is(err, authority.ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
	if enrolled, err := rig.Provider.Enrolled(ctx, rig.Scope); err != nil || enrolled {
		t.Fatalf("enrolled %v, %v", enrolled, err)
	}
}

func checkIdentitySecondEnrollment(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	runs := 0
	err := rig.Provider.Enroll(ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, rig.Anyone, accompany(rig, &runs))
	if !errors.Is(err, authority.ErrNoSession) {
		t.Fatalf("got %v, want ErrNoSession", err)
	}
	if runs != 0 || companionWritten(t, ctx, rig) {
		t.Errorf("a refused set ran its companion %d times", runs)
	}
	if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, nil); err != nil {
		t.Fatalf("the refused change moved the passphrase: %v", err)
	}
	var told string
	companion := func(tx ports.SharedTx, member string) error {
		told = member
		return accompany(rig, &runs)(tx, member)
	}
	if err := rig.Provider.Enroll(ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, rig.Member, companion); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || !companionWritten(t, ctx, rig) {
		t.Fatalf("the companion ran %d times, and its event is written: %v", runs, companionWritten(t, ctx, rig))
	}
	member, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, nil)
	if err != nil {
		t.Fatalf("the new passphrase: %v", err)
	}
	if told != member {
		t.Errorf("the companion was told member %q, and the passphrase names %q", told, member)
	}
	if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, nil); !errors.Is(err, authority.ErrBadProof) {
		t.Fatalf("the old passphrase: got %v, want ErrBadProof", err)
	}
}

// accompany is a change's companion write: one event, which stands or falls with the new proof. It counts
// its runs, so a check can tell a refusal that never reached it from one that undid it.
func accompany(rig IdentityRig, runs *int) func(ports.SharedTx, string) error {
	return func(tx ports.SharedTx, member string) error {
		*runs++
		_, err := tx.Append(ledger.EventDraft{Type: companionEvent, Actor: rig.Member, Subject: ledger.Subject{Kind: "member", ID: member}})
		return err
	}
}

// companionWritten says whether the companion's event is in the installation's log.
func companionWritten(t *testing.T, ctx context.Context, rig IdentityRig) bool {
	t.Helper()
	var found bool
	err := rig.Store.Installation(rig.Scope).Read(ctx, func(tx ports.SharedReadTx) error {
		events, err := tx.Events(0, 1000)
		for _, event := range events {
			found = found || event.Type == companionEvent
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// onlyTheOldStands holds that the passphrase is still goodPassphrase, and no later one proves anything.
func onlyTheOldStands(t *testing.T, ctx context.Context, rig IdentityRig) {
	t.Helper()
	if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, nil); err != nil {
		t.Fatalf("the refused change moved the passphrase: %v", err)
	}
	for _, later := range []string{otherPassphrase, thirdPassphrase} {
		if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: later}, nil); !errors.Is(err, authority.ErrBadProof) {
			t.Fatalf("%q proves the member after a refused change: %v", later, err)
		}
	}
}

func checkIdentityChangeNeedsTheCurrent(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	runs := 0
	for _, wrong := range []string{"", otherPassphrase, goodPassphrase + " ", thirdPassphrase} {
		err := rig.Provider.Change(ctx, rig.Scope, ports.Proof{Passphrase: wrong}, ports.Proof{Passphrase: otherPassphrase}, rig.Member, accompany(rig, &runs))
		if !errors.Is(err, authority.ErrBadProof) {
			t.Errorf("current %q: got %v, want ErrBadProof", wrong, err)
		}
	}
	if runs != 0 || companionWritten(t, ctx, rig) {
		t.Errorf("a refused change ran its companion %d times", runs)
	}
	onlyTheOldStands(t, ctx, rig)
}

func checkIdentityChangeUnenrolled(t *testing.T, ctx context.Context, rig IdentityRig) {
	runs := 0
	err := rig.Provider.Change(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, rig.Member, accompany(rig, &runs))
	if !errors.Is(err, authority.ErrNotEnrolled) {
		t.Fatalf("got %v, want ErrNotEnrolled", err)
	}
	if enrolled, err := rig.Provider.Enrolled(ctx, rig.Scope); err != nil || enrolled {
		t.Fatalf("a refused change set a passphrase: enrolled %v, %v", enrolled, err)
	}
	if runs != 0 {
		t.Errorf("a refused change ran its companion %d times", runs)
	}
}

func checkIdentityChangeNeedsAMember(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	runs := 0
	err := rig.Provider.Change(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, rig.Anyone, accompany(rig, &runs))
	if !errors.Is(err, authority.ErrNoSession) {
		t.Fatalf("got %v, want ErrNoSession", err)
	}
	if runs != 0 {
		t.Errorf("a refused change ran its companion %d times", runs)
	}
	onlyTheOldStands(t, ctx, rig)
}

func checkIdentityChangeRefusesAShortProof(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	runs := 0
	err := rig.Provider.Change(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: "short"}, rig.Member, accompany(rig, &runs))
	if !errors.Is(err, authority.ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
	if runs != 0 || companionWritten(t, ctx, rig) {
		t.Errorf("a refused change ran its companion %d times", runs)
	}
	onlyTheOldStands(t, ctx, rig)
	// A short new proof beside a wrong current one says the current one is wrong: nothing is learned from the length.
	err = rig.Provider.Change(ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, ports.Proof{Passphrase: "short"}, rig.Member, nil)
	if !errors.Is(err, authority.ErrBadProof) {
		t.Fatalf("a wrong current passphrase with a short new one: got %v, want ErrBadProof", err)
	}
}

func checkIdentityChangeCompanion(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	runs := 0
	var told string
	companion := func(tx ports.SharedTx, member string) error {
		told = member
		return accompany(rig, &runs)(tx, member)
	}
	if err := rig.Provider.Change(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, rig.Member, companion); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || !companionWritten(t, ctx, rig) {
		t.Fatalf("the companion ran %d times, and its event is written: %v", runs, companionWritten(t, ctx, rig))
	}
	member, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, nil)
	if err != nil {
		t.Fatalf("the new passphrase: %v", err)
	}
	if told != member {
		t.Errorf("the companion was told member %q, and the passphrase names %q", told, member)
	}
	if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, nil); !errors.Is(err, authority.ErrBadProof) {
		t.Fatalf("the old passphrase: got %v, want ErrBadProof", err)
	}
	// The new passphrase is now the current one, and the old one cannot change it again.
	if err := rig.Provider.Change(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: thirdPassphrase}, rig.Member, nil); !errors.Is(err, authority.ErrBadProof) {
		t.Fatalf("the old passphrase changed it again: %v", err)
	}
	if err := rig.Provider.Change(ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, ports.Proof{Passphrase: thirdPassphrase}, rig.Member, nil); err != nil {
		t.Fatalf("a change with no companion: %v", err)
	}
}

func checkIdentityRefusesATooLongProof(t *testing.T, ctx context.Context, rig IdentityRig) {
	tooLong := strings.Repeat("a", authority.MaxPassphrase+1)
	longest := strings.Repeat("b", authority.MaxPassphrase)
	if err := rig.Provider.Enroll(ctx, rig.Scope, ports.Proof{Passphrase: tooLong}, rig.Anyone, nil); !errors.Is(err, authority.ErrInvalid) {
		t.Fatalf("the first set of %d bytes: got %v, want ErrInvalid", len(tooLong), err)
	}
	if enrolled, err := rig.Provider.Enrolled(ctx, rig.Scope); err != nil || enrolled {
		t.Fatalf("a refused first set set a passphrase: %v, %v", enrolled, err)
	}
	enroll(t, ctx, rig)
	if err := rig.Provider.Enroll(ctx, rig.Scope, ports.Proof{Passphrase: tooLong}, rig.Member, nil); !errors.Is(err, authority.ErrInvalid) {
		t.Fatalf("a later set of %d bytes: got %v, want ErrInvalid", len(tooLong), err)
	}
	if err := rig.Provider.Change(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: tooLong}, rig.Member, nil); !errors.Is(err, authority.ErrInvalid) {
		t.Fatalf("a change to %d bytes: got %v, want ErrInvalid", len(tooLong), err)
	}
	onlyTheOldStands(t, ctx, rig)
	if err := rig.Provider.Change(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: longest}, rig.Member, nil); err != nil {
		t.Fatalf("a change to %d bytes, the longest allowed: %v", len(longest), err)
	}
	if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: longest}, nil); err != nil {
		t.Fatalf("the longest passphrase: %v", err)
	}
}

func checkIdentitySignInCompanion(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	runs := 0
	var told string
	companion := func(tx ports.SharedTx, member string) error {
		told = member
		return accompany(rig, &runs)(tx, member)
	}
	member, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, companion)
	if err != nil || member == "" {
		t.Fatalf("member %q, %v", member, err)
	}
	if runs != 1 || !companionWritten(t, ctx, rig) {
		t.Fatalf("the companion ran %d times, and its event is written: %v", runs, companionWritten(t, ctx, rig))
	}
	if told != member {
		t.Errorf("the companion was told member %q, and the passphrase names %q", told, member)
	}
	// A wrong passphrase runs no companion.
	if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, accompany(rig, &runs)); !errors.Is(err, authority.ErrBadProof) || runs != 1 {
		t.Errorf("a wrong passphrase: %v, and the companion ran %d times", err, runs)
	}
}

// checkIdentitySignInOvertakenByAChange lands a change between a sign-in's check of the passphrase and its write. The
// passphrase the sign-in proved is no longer in force when it would write, so what its companion would write, a
// session, must not stand: the change has ended every session it knew of. The passphrase was right when it was
// checked, so the refusal is the one a change that lost gets, which is no wrong guess.
func checkIdentitySignInOvertakenByAChange(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	armed := true
	rig.Store.Before = func(string) {
		if !armed {
			return
		}
		armed = false
		if err := rig.Provider.Change(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, rig.Member, nil); err != nil {
			t.Errorf("the change that lands first: %v", err)
		}
	}
	runs := 0
	member, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, accompany(rig, &runs))
	rig.Store.Before = nil
	if armed {
		t.Fatal("the sign-in made no write for the change to land before")
	}
	if !errors.Is(err, authority.ErrPassphraseChanged) || member != "" {
		t.Fatalf("member %q, %v; want ErrPassphraseChanged", member, err)
	}
	if runs != 0 || companionWritten(t, ctx, rig) {
		t.Errorf("an overtaken sign-in ran its companion %d times", runs)
	}
	if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, accompany(rig, &runs)); err != nil || runs != 1 {
		t.Errorf("the passphrase that landed first: %v, and the companion ran %d times", err, runs)
	}
}

func checkIdentitySecondEnrollmentIsAtomic(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	broken := errors.New("the companion failed")
	runs := 0
	err := rig.Provider.Enroll(ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, rig.Member, func(tx ports.SharedTx, member string) error {
		if err := accompany(rig, &runs)(tx, member); err != nil {
			return err
		}
		return broken
	})
	if !errors.Is(err, broken) {
		t.Fatalf("got %v, want the companion's error", err)
	}
	if runs != 1 {
		t.Fatalf("the companion ran %d times", runs)
	}
	if companionWritten(t, ctx, rig) {
		t.Error("the companion's write stood after its set failed")
	}
	onlyTheOldStands(t, ctx, rig)
}

func checkIdentityChangeIsAtomic(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	broken := errors.New("the companion failed")
	runs := 0
	err := rig.Provider.Change(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, ports.Proof{Passphrase: otherPassphrase}, rig.Member, func(tx ports.SharedTx, member string) error {
		if err := accompany(rig, &runs)(tx, member); err != nil {
			return err
		}
		return broken
	})
	if !errors.Is(err, broken) {
		t.Fatalf("got %v, want the companion's error", err)
	}
	if runs != 1 {
		t.Fatalf("the companion ran %d times", runs)
	}
	if companionWritten(t, ctx, rig) {
		t.Error("the companion's write stood after its change failed")
	}
	onlyTheOldStands(t, ctx, rig)
}
