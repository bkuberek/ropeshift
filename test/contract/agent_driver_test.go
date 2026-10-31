package contract

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/claudecode"
	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/adapters/secrets"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/apitest"
	"ropeshift.dev/ropeshift/test/doubles"
)

func baseSpec(t *testing.T) ports.SessionSpec {
	t.Helper()
	return ports.SessionSpec{
		Lane: "lane-1", WorkOrder: "wo", Generation: 1, Dir: t.TempDir(), Model: "opus", PermissionMode: "default",
		Brief: "the brief", ConfigDir: filepath.Join(t.TempDir(), "generated"),
	}
}

func TestClaudeCodeDriverContract(t *testing.T) {
	RunAgentDriver(t, func(t *testing.T) DriverRig {
		driver := claudecode.New(osexec.Runner{}, claudecode.Options{})
		arrange := func(scenario string) ports.SessionSpec {
			path := doubles.InstallFakeClaude(t, scenario)
			driver.UseEnv([]string{"PATH=" + path})
			spec := baseSpec(t)
			spec.Env = []string{"PATH=" + path, "HOME=" + t.TempDir()}
			spec.Binary, spec.TempDir = doubles.Ropeshift(t), apitest.ShortDir(t)
			doubles.ReapShims(t, spec.ConfigDir)
			return spec
		}
		arrange(ScenarioTwoTurns)
		return DriverRig{Driver: driver, Arrange: arrange}
	})
}

func fakeTurn(total float64) fake.DriverStep {
	return fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: total}}
}

// fakeScenarios are the contract's scenarios in the fake's terms.
var fakeScenarios = map[string][][]fake.DriverStep{
	ScenarioTwoTurns: {
		{{Bound: true}, {Context: &lanes.Usage{InputTokens: 3, CacheCreationTokens: 100, CacheReadTokens: 900}}, fakeTurn(0.1)},
		{{Bound: true}, {Context: &lanes.Usage{InputTokens: 3, CacheCreationTokens: 10, CacheReadTokens: 1100}}, fakeTurn(0.25)},
	},
	ScenarioStopped:   {{{Bound: true}, {Context: &lanes.Usage{InputTokens: 3}}, {Hold: true}}},
	ScenarioCrash:     {{{Bound: true}, {Context: &lanes.Usage{InputTokens: 3}}, {Crash: true}}},
	ScenarioMalformed: {{{Bound: true}, {Malformed: true}, {Context: &lanes.Usage{InputTokens: 7}}, fakeTurn(0.01)}},
	ScenarioFailedTurn: {{{Bound: true}, {Turn: &fake.DriverTurn{
		Status: ports.TurnFailed, Reason: "error_max_turns", TotalUSD: 0.02,
	}}}},
	ScenarioSubagent: {{{Bound: true}, {Context: &lanes.Usage{InputTokens: 11}}, fakeTurn(0.01)}},
}

func TestFakeDriverContract(t *testing.T) {
	RunAgentDriver(t, func(t *testing.T) DriverRig {
		driver := fake.NewAgentDriver()
		return DriverRig{Driver: driver, Arrange: func(scenario string) ports.SessionSpec {
			driver.Play(fake.DriverScript{SessionID: "session-1", Model: "claude-fixture-5-5", Turns: fakeScenarios[scenario]})
			return baseSpec(t)
		}}
	})
}

// TestSecretPortReturnsNoValue guards D9. The binding has no field that could carry a
// key, and no resolver hands one back, even with the variable set.
func TestSecretPortReturnsNoValue(t *testing.T) {
	binding := reflect.TypeFor[ports.SecretBinding]()
	if binding.NumField() != 2 {
		t.Errorf("the binding has %d fields, so a new one needs this check", binding.NumField())
	}
	for field := range binding.Fields() {
		name := strings.ToLower(field.Name)
		for _, word := range []string{"value", "secret", "key "} {
			if strings.Contains(name, word) {
				t.Errorf("field %s reads like a value", field.Name)
			}
		}
	}
	const sentinel = "a-key-that-must-not-travel"
	t.Setenv("ROPESHIFT_TEST_API_KEY", sentinel)
	resolvers := map[string]ports.SecretResolver{"fake": fake.SecretResolver{}, "real": secrets.Resolver{}}
	for name, resolver := range resolvers {
		t.Run(name, func(t *testing.T) {
			got, err := resolver.Resolve(t.Context(), "env:ROPESHIFT_TEST_API_KEY")
			if err != nil {
				t.Fatal(err)
			}
			if got.EnvName != "ROPESHIFT_TEST_API_KEY" || got.APIKeyHelper != "" {
				t.Errorf("binding %+v", got)
			}
			if empty, err := resolver.Resolve(t.Context(), ""); err != nil || empty != (ports.SecretBinding{}) {
				t.Errorf("no reference: %+v, %v", empty, err)
			}
			if strings.Contains(strings.Join([]string{got.EnvName, got.APIKeyHelper}, " "), sentinel) {
				t.Error("a resolver returned the value")
			}
		})
	}
}
