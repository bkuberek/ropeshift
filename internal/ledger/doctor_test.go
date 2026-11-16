package ledger_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

func TestADriverResultStatesUsableAsAnOfferReadsIt(t *testing.T) {
	present := ledger.DriverManifest{
		Driver: "claude-code", Present: true, Version: "2.1.316", Qualified: true,
		SignIn: ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedIn},
	}
	unread := present
	unread.SignIn = ledger.SignIn{Status: ledger.SignInUnknown}
	signedOut := present
	signedOut.SignIn = ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedOut}
	signedOutNoKind := present
	signedOutNoKind.SignIn = ledger.SignIn{Status: ledger.SignedOut}
	absent := ledger.DriverManifest{Driver: "claude-code", Detail: "claude is not on PATH"}
	failed := ledger.DriverManifest{Driver: "claude-code", Detail: "claude --version exited with status 1"}
	unknownVendor := ledger.DriverManifest{Driver: "other-cli", Detail: "other-cli --version ended on a signal"}

	tests := []struct {
		name   string
		driver ledger.DriverManifest
		want   ledger.DriverResult
	}{
		{"present and signed in", present, ledger.DriverResult{Driver: "claude-code", Usable: true}},
		{"a sign-in nobody could read stays usable", unread, ledger.DriverResult{Driver: "claude-code", Usable: true}},
		{"absent says why in the doctor's words", absent, ledger.DriverResult{Driver: "claude-code", Word: "absent", Reason: "claude is not on PATH"}},
		{
			"a CLI that failed its version check says the fix", failed,
			ledger.DriverResult{
				Driver: "claude-code", Word: "unusable", Reason: "claude --version exited with status 1",
				Fix: "Run claude --version to see why, and repair or reinstall Claude Code.",
			},
		},
		{"signed out names the sign-in kind", signedOut, ledger.DriverResult{Driver: "claude-code", Word: "signed out", Reason: "sign-in subscription"}},
		{"signed out with no kind says there is none", signedOutNoKind, ledger.DriverResult{Driver: "claude-code", Word: "signed out", Reason: "no sign-in"}},
		{
			"a CLI no table names is called by its own name", unknownVendor,
			ledger.DriverResult{Driver: "other-cli", Word: "unusable", Reason: "other-cli --version ended on a signal", Fix: "Run other-cli --version to see why, and repair or reinstall other-cli."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.driver.Result(); got != tt.want {
				t.Fatalf("result %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestADriverIsUsableByTheRuleOfTheOffer(t *testing.T) {
	// The result and the offer must never disagree, so each is held to the other over every shape of driver.
	for _, present := range []bool{false, true} {
		for _, status := range []string{ledger.SignedIn, ledger.SignedOut, ledger.SignInUnknown, ""} {
			driver := ledger.DriverManifest{Driver: "claude-code", Present: present, SignIn: ledger.SignIn{Status: status}}
			offered, _ := ledger.Manifest{Drivers: []ledger.DriverManifest{driver}}.Runs(map[string]string{ledger.NeedDriver: "claude-code"})
			if got := driver.Result().Usable; got != offered {
				t.Errorf("present %v, sign-in %q: the result says usable %v, and an offer says %v", present, status, got, offered)
			}
		}
	}
}

func TestAManifestGivesOneResultForEachDriverInOrder(t *testing.T) {
	manifest := ledger.Manifest{Drivers: []ledger.DriverManifest{
		{Driver: "b", Present: true, SignIn: ledger.SignIn{Status: ledger.SignedIn}},
		{Driver: "a", Detail: "a is not on PATH"},
	}}
	results := manifest.Results()
	if len(results) != 2 || results[0].Driver != "b" || !results[0].Usable || results[1].Driver != "a" || results[1].Word != ledger.WordAbsent {
		t.Fatalf("results %+v", results)
	}
	if got := (ledger.Manifest{}).Results(); got == nil || len(got) != 0 {
		t.Fatalf("a manifest that names no driver gives %#v, which is an empty list a client can read", got)
	}
}

func TestAVendorKnownToTheTableNamesItsCommand(t *testing.T) {
	if v := ledger.VendorOf("claude-code"); v.Product != "Claude Code" || v.Command != "claude" || v.Company != "Anthropic" {
		t.Errorf("vendor %+v", v)
	}
	if v := ledger.VendorOf("other-cli"); v.Product != "other-cli" || v.Command != "other-cli" || v.Company != "its vendor" {
		t.Errorf("vendor %+v", v)
	}
}
