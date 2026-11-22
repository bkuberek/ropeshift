package ghcli

import (
	"strings"
	"testing"
)

var plantedSecrets = map[string]string{
	"ROPESHIFT_HOOK_NOTIFY":              "planted-webhook-key",
	"ROPESHIFT_MEMBER_TOKEN":             "planted-member-token",
	"ROPESHIFT_DATABASE":                 "planted-database-address",
	"OTEL_EXPORTER_OTLP_HEADERS":         "planted-headers",
	"OTEL_EXPORTER_OTLP_METRICS_HEADERS": "planted-metric-headers",
	"PGPASSWORD":                         "planted-database-password",
}

func checkEnvironment(t *testing.T, env []string) {
	t.Helper()
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for name, value := range plantedSecrets {
		if strings.Contains(joined, "\n"+name+"=") || strings.Contains(joined, value) {
			t.Errorf("gh would see %s", name)
		}
	}
	for _, want := range []string{"\nGH_TOKEN=planted-gh-token\n", "\nPATH=", "\nGH_HOST=github.example\n"} {
		if !strings.Contains(joined, want) {
			t.Errorf("gh lost %q", strings.TrimSpace(want))
		}
	}
}

func TestForgeEnvironmentWithholdsTheDaemonsSecrets(t *testing.T) {
	env := []string{"GH_TOKEN=planted-gh-token", "PATH=/usr/bin", "GH_HOST=github.example"}
	for name, value := range plantedSecrets {
		env = append(env, name+"="+value)
	}
	checkEnvironment(t, Forge{Env: env}.environment())
}

func TestForgeEnvironmentFromTheProcessWithholdsTheDaemonsSecrets(t *testing.T) {
	for name, value := range plantedSecrets {
		t.Setenv(name, value)
	}
	t.Setenv("GH_TOKEN", "planted-gh-token")
	t.Setenv("GH_HOST", "github.example")
	t.Setenv("PATH", "/usr/bin")
	checkEnvironment(t, Forge{}.environment())
}

func TestAccountsForHostWithholdsTheDaemonsSecrets(t *testing.T) {
	for name, value := range plantedSecrets {
		t.Setenv(name, value)
	}
	t.Setenv("GH_TOKEN", "planted-gh-token")
	t.Setenv("PATH", "/usr/bin")
	checkEnvironment(t, Accounts{}.forHost("github.example").environment())
}
