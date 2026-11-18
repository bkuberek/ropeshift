package server_test

import (
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
)

// A member who publishes a notify policy that signs its webhook with a variable of the daemon's environment that holds no
// webhook key, such as the database's address, is refused with the prefix the variable must start with, and a variable that
// starts with it is accepted.
func TestAWebhookIsSignedOnlyWithAVariableThatMayHoldAWebhookKey(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	policy := func(version, name string) string {
		return "[notify_policy]\nversion = " + version + "\nwebhook_url = \"https://hooks.example.com/r\"\nwebhook_secret_env = \"" + name + "\"\n"
	}
	_, err := d.client.PublishPolicy(ctx, policy("2", "DATABASE_URL"))
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	if !strings.Contains(err.Error(), "ROPESHIFT_HOOK_") {
		t.Errorf("the refusal reads %q, and it does not name the prefix", err)
	}
	if _, err := d.client.PublishPolicy(ctx, policy("2", "ROPESHIFT_HOOK_NOTIFY")); err != nil {
		t.Errorf("a variable that starts with the prefix was refused: %v", err)
	}
}
