package control

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
)

type fixedRepos map[string][]CIRegistration

func (r fixedRepos) CIRepository(_ context.Context, host, owner, repo string) ([]CIRegistration, error) {
	return r[CIKey(host, owner, repo)], nil
}

func signatureOf(key string, body []byte) string {
	return "sha256=" + hex.EncodeToString(hmacSHA256(key, body))
}

// A request costs the same whether or not the repository it names is registered, and whether or not it has a key: the
// digest of the body is computed once, under a fixed dummy key when there is no key of the repository's to use, so
// the time a refusal takes tells a caller nothing of which repositories there are.
func TestEveryDeliveryThatNamesARepositoryComputesOneDigestOverTheBody(t *testing.T) {
	t.Parallel()
	widgets := ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}
	const key = "the secret of acme widgets"
	body := []byte(`{"action":"completed","check_suite":{"head_sha":"0123456789abcdef0123456789abcdef01234567"},"repository":{"full_name":"acme/widgets"}}`)
	held := fixedRepos{CIKey("github.com", "acme", "widgets"): {{Binding: widgets, SecretEnv: "ROPESHIFT_HOOK_W"}}}
	tests := map[string]struct {
		repos     fixedRepos
		env       map[string]string
		signature string
		keyed     bool
	}{
		"a repository nobody registered":   {fixedRepos{}, nil, signatureOf(key, body), false},
		"a repository with no variable":    {fixedRepos{CIKey("github.com", "acme", "widgets"): {{Binding: widgets}}}, nil, signatureOf(key, body), false},
		"a variable the server lacks":      {held, nil, signatureOf(key, body), false},
		"a variable set to nothing":        {held, map[string]string{"ROPESHIFT_HOOK_W": ""}, signatureOf(key, body), false},
		"a registered key and a bad guess": {held, map[string]string{"ROPESHIFT_HOOK_W": key}, signatureOf("a guess", body), true},
		"a registered key and its digest":  {held, map[string]string{"ROPESHIFT_HOOK_W": key}, signatureOf(key, body), true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			now := time.UnixMilli(1_700_000_000_000)
			clock := func() time.Time { return now }
			c := NewCIWebhooks(tt.repos, NewCIHints(clock), func(name string) (string, bool) { v, ok := tt.env[name]; return v, ok }, clock, nil)
			var keys []string
			c.digest = func(key string, body []byte) []byte {
				keys = append(keys, key)
				return hmacSHA256(key, body)
			}
			_, _ = c.Receive(t.Context(), CIDelivery{Event: "check_suite", ID: "d-1", Signature: tt.signature, Body: body})
			if len(keys) != 1 {
				t.Fatalf("%d digests were computed over the body, want 1", len(keys))
			}
			if (keys[0] == key) != tt.keyed {
				t.Errorf("the digest was made under the repository's key: %v, want %v", keys[0] == key, tt.keyed)
			}
		})
	}
}
