package contract

import (
	"context"
	"errors"
	"testing"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

// IdentityRig is one provider and who calls it.
type IdentityRig struct {
	Provider ports.IdentityProvider
	Scope    ledger.WorkspaceID
	// Member is a caller with a session.
	Member ledger.Actor
	// Anyone is a caller with no session.
	Anyone ledger.Actor
}

const (
	goodPassphrase  = "a passphrase only a person knows"
	otherPassphrase = "a different passphrase entirely"
)

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
		{"a second passphrase needs a member", checkIdentitySecondEnrollment},
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
	if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}); !errors.Is(err, authority.ErrNotEnrolled) {
		t.Fatalf("got %v, want ErrNotEnrolled", err)
	}
}

func enroll(t *testing.T, ctx context.Context, rig IdentityRig) {
	t.Helper()
	if err := rig.Provider.Enroll(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, rig.Anyone); err != nil {
		t.Fatal(err)
	}
}

func checkIdentityAuthenticates(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	enrolled, err := rig.Provider.Enrolled(ctx, rig.Scope)
	if err != nil || !enrolled {
		t.Fatalf("enrolled %v, %v", enrolled, err)
	}
	member, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase})
	if err != nil || member == "" {
		t.Fatalf("member %q, %v", member, err)
	}
}

func checkIdentityRefusesAWrongProof(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	for _, wrong := range []string{"", otherPassphrase, goodPassphrase + " "} {
		if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: wrong}); !errors.Is(err, authority.ErrBadProof) {
			t.Errorf("%q: got %v, want ErrBadProof", wrong, err)
		}
	}
}

func checkIdentityRefusesAShortProof(t *testing.T, ctx context.Context, rig IdentityRig) {
	err := rig.Provider.Enroll(ctx, rig.Scope, ports.Proof{Passphrase: "short"}, rig.Anyone)
	if !errors.Is(err, authority.ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
	if enrolled, err := rig.Provider.Enrolled(ctx, rig.Scope); err != nil || enrolled {
		t.Fatalf("enrolled %v, %v", enrolled, err)
	}
}

func checkIdentitySecondEnrollment(t *testing.T, ctx context.Context, rig IdentityRig) {
	enroll(t, ctx, rig)
	err := rig.Provider.Enroll(ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, rig.Anyone)
	if !errors.Is(err, authority.ErrNoSession) {
		t.Fatalf("got %v, want ErrNoSession", err)
	}
	if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}); err != nil {
		t.Fatalf("the refused change moved the passphrase: %v", err)
	}
	if err := rig.Provider.Enroll(ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}, rig.Member); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: otherPassphrase}); err != nil {
		t.Fatalf("the new passphrase: %v", err)
	}
	if _, err := rig.Provider.Authenticate(ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}); !errors.Is(err, authority.ErrBadProof) {
		t.Fatalf("the old passphrase: got %v, want ErrBadProof", err)
	}
}
