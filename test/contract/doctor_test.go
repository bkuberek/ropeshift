package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/claudecode"
	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/test/doubles"
)

// identifying is what `claude auth status` prints that names a person or a machine.
var identifying = []string{"person@example.invalid", "org-fixture", "Fixture Organization", "fixture-plan", "/fixture/config"}

// TestDoctorSendsOnlyTheKind runs the real driver against every documented `authMethod`. A
// fixture sets what the status command answers, and what it says beside the method must
// stop here: the manifest is what leaves the machine.
func TestDoctorSendsOnlyTheKind(t *testing.T) {
	tests := []struct {
		method, kind, status string
	}{
		{"none", "", ledger.SignedOut},
		{"claude.ai", ledger.SignInSubscription, ledger.SignedIn},
		{"oauth_token", ledger.SignInSubscription, ledger.SignedIn},
		{"api_key", ledger.SignInAPIKey, ledger.SignedIn},
		{"api_key_helper", ledger.SignInAPIKey, ledger.SignedIn},
		{"third_party", ledger.SignInAPIKey, ledger.SignedIn},
		{"a-word-from-a-later-version", "", ledger.SignInUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			path := doubles.InstallFakeClaude(t, "no-session")
			fixture := filepath.Join(strings.SplitN(path, string(os.PathListSeparator), 2)[0], doubles.FixtureFile)
			writeAuthMethod(t, fixture, tt.method)
			driver := claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + path}})
			manifest, err := worker.BuildManifest(t.Context(), driver, ledger.Manifest{})
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.Drivers) != 1 {
				t.Fatalf("manifest %+v", manifest)
			}
			got := manifest.Drivers[0]
			if !got.Present || got.Driver != "claude-code" || got.SignIn.Kind != tt.kind || got.SignIn.Status != tt.status {
				t.Errorf("driver %+v", got)
			}
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			for _, word := range identifying {
				if strings.Contains(string(raw), word) {
					t.Errorf("the manifest carries %q: %s", word, raw)
				}
			}
		})
	}
}

func writeAuthMethod(t *testing.T, path, method string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"source": "T: a documented authMethod", "auth_method": method})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorWithNoCLISaysSo(t *testing.T) {
	driver := claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + t.TempDir()}})
	manifest, err := worker.BuildManifest(t.Context(), driver, ledger.Manifest{})
	if err != nil {
		t.Fatal(err)
	}
	got := manifest.Drivers[0]
	if got.Present || got.Detail == "" {
		t.Errorf("driver %+v", got)
	}
	if runs, _ := manifest.Runs(map[string]string{ledger.NeedDriver: "claude-code"}); runs {
		t.Error("a machine with no CLI is offered work")
	}
}
