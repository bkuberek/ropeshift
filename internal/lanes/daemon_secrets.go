package lanes

import (
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

// daemonSecrets names the variables of the daemon's own environment that hold a key, a token, or a
// connection string, and no child of the daemon may read: a lane through an `env:` reference, or a git
// command the daemon runs.
var daemonSecrets = []string{
	authority.EnvMemberToken, authority.EnvDelegateToken,
	"ROPESHIFT_DATABASE", "ROPESHIFT_JOIN", "ROPESHIFT_TLS_KEY",
	"OTEL_EXPORTER_OTLP_HEADERS",
}

// DaemonSecret reports a variable that holds a secret of the daemon: a webhook key, a member or delegate
// token, the database address, the join string, the TLS key, or the telemetry exporter's headers, whether
// shared or for one signal.
func DaemonSecret(name string) bool {
	if strings.HasPrefix(name, ledger.WebhookSecretEnvPrefix) {
		return true
	}
	if rest, ok := strings.CutPrefix(name, "OTEL_EXPORTER_OTLP_"); ok && strings.HasSuffix(rest, "_HEADERS") {
		return true
	}
	for _, secret := range daemonSecrets {
		if name == secret {
			return true
		}
	}
	return false
}
