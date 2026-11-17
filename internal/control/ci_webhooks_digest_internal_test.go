package control

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
)

// A repository that several projects hold costs one digest for each distinct key, not one for each project, so the
// time a delivery takes does not say how many projects hold the repository it names.
func TestAWebhookForARepositoryInTwoProjectsWithTheSameKeyCostsOneDigest(t *testing.T) {
	t.Parallel()
	widgets := ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}
	const key = "the secret of acme widgets"
	body := []byte(`{"action":"completed","check_suite":{"head_sha":"0123456789abcdef0123456789abcdef01234567"},"repository":{"full_name":"acme/widgets"}}`)
	tests := map[string]struct {
		registered []CIRegistration
		env        map[string]string
		digests    int
	}{
		"one variable in two projects": {
			[]CIRegistration{{Project: "pa", Binding: widgets, SecretEnv: "HOOK_A"}, {Project: "pb", Binding: widgets, SecretEnv: "HOOK_A"}},
			map[string]string{"HOOK_A": key}, 1,
		},
		"two variables of one value": {
			[]CIRegistration{{Project: "pa", Binding: widgets, SecretEnv: "HOOK_A"}, {Project: "pb", Binding: widgets, SecretEnv: "HOOK_B"}},
			map[string]string{"HOOK_A": key, "HOOK_B": key}, 1,
		},
		"two variables of two values": {
			[]CIRegistration{{Project: "pa", Binding: widgets, SecretEnv: "HOOK_A"}, {Project: "pb", Binding: widgets, SecretEnv: "HOOK_B"}},
			map[string]string{"HOOK_A": key, "HOOK_B": "another secret"}, 2,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			now := time.UnixMilli(1_700_000_000_000)
			clock := func() time.Time { return now }
			repos := fixedRepos{CIKey("github.com", "acme", "widgets"): tt.registered}
			c := NewCIWebhooks(repos, NewCIHints(clock), func(name string) (string, bool) { v, ok := tt.env[name]; return v, ok }, clock, nil)
			digests := 0
			c.digest = func(key string, body []byte) []byte {
				digests++
				return hmacSHA256(key, body)
			}
			outcome, err := c.Receive(t.Context(), CIDelivery{Event: "check_suite", ID: "d-1", Signature: signatureOf(key, body), Body: body})
			if err != nil || outcome != CIAccepted {
				t.Fatalf("a delivery signed with the key was %q, %v", outcome, err)
			}
			if digests != tt.digests {
				t.Errorf("%d digests were computed over the body, want %d", digests, tt.digests)
			}
			hints, _ := c.hints.Since(0)
			if tt.digests == 1 && len(hints) != 2 {
				t.Errorf("a key that holds for both projects made %d hints, want one for each project", len(hints))
			}
		})
	}
}
