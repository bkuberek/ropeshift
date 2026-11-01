package ledger_test

import (
	"testing"

	"ropeshift.dev/ropeshift/internal/ledger"
)

func TestManifestRuns(t *testing.T) {
	subscription := ledger.DriverManifest{
		Driver: "claude-code", Present: true, Version: "2.1.316", Qualified: true,
		SignIn: ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedIn},
	}
	signedOut := subscription
	signedOut.SignIn = ledger.SignIn{Status: ledger.SignedOut}
	absent := ledger.DriverManifest{Driver: "claude-code"}
	unread := subscription
	unread.SignIn = ledger.SignIn{Status: ledger.SignInUnknown}
	tests := []struct {
		name     string
		manifest ledger.Manifest
		needs    map[string]string
		want     bool
	}{
		{name: "work that needs nothing runs anywhere", want: true},
		{name: "a driver it has", manifest: ledger.Manifest{Drivers: []ledger.DriverManifest{subscription}}, needs: map[string]string{"driver": "claude-code"}, want: true},
		{name: "a driver it does not have", manifest: ledger.Manifest{Drivers: []ledger.DriverManifest{subscription}}, needs: map[string]string{"driver": "codex"}},
		{name: "a driver with no CLI on the machine", manifest: ledger.Manifest{Drivers: []ledger.DriverManifest{absent}}, needs: map[string]string{"driver": "claude-code"}},
		{name: "a driver signed out", manifest: ledger.Manifest{Drivers: []ledger.DriverManifest{signedOut}}, needs: map[string]string{"driver": "claude-code"}},
		{name: "a sign-in it could not read is no evidence", manifest: ledger.Manifest{Drivers: []ledger.DriverManifest{unread}}, needs: map[string]string{"driver": "claude-code"}, want: true},
		{name: "a worker that advertised nothing", needs: map[string]string{"driver": "claude-code"}},
		{name: "the sign-in kind asked for", manifest: ledger.Manifest{Drivers: []ledger.DriverManifest{subscription}}, needs: map[string]string{"sign_in": "subscription"}, want: true},
		{name: "another sign-in kind", manifest: ledger.Manifest{Drivers: []ledger.DriverManifest{subscription}}, needs: map[string]string{"sign_in": "api_key"}},
		{name: "a label it carries", manifest: ledger.Manifest{Labels: map[string]string{"os": "linux"}}, needs: map[string]string{"os": "linux"}, want: true},
		{name: "a label with another value", manifest: ledger.Manifest{Labels: map[string]string{"os": "darwin"}}, needs: map[string]string{"os": "linux"}},
		{name: "a label it does not carry", needs: map[string]string{"gpu": "yes"}},
		{name: "every need must hold", manifest: ledger.Manifest{Drivers: []ledger.DriverManifest{subscription}, Labels: map[string]string{"os": "linux"}}, needs: map[string]string{"driver": "claude-code", "os": "darwin"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, why := tt.manifest.Runs(tt.needs)
			if got != tt.want {
				t.Errorf("runs %v (%s), want %v", got, why, tt.want)
			}
		})
	}
}
