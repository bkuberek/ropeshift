package authority_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/roster"
)

// The installation's spend policy has the two keys the design states, each with its default, and its built-in
// body says both.
func TestTheBuiltInInstallationSpendPolicyStatesItsKeys(t *testing.T) {
	got := authority.DefaultInstallationSpendPolicy()
	if got.Version != 1 || got.OrchestratorClass != "standard" || got.OrchestratorDailyWarn != 0 {
		t.Errorf("the built-in policy reads %+v", got)
	}
	if err := got.Check(); err != nil {
		t.Errorf("the built-in policy does not check: %v", err)
	}
	if body := got.Body(); body != authority.BuiltInInstallationSpendPolicyBody {
		t.Errorf("the built-in body is not the default policy stated:\n%s", body)
	}
	for _, line := range []string{"[installation_spend_policy]", "version = 1", `orchestrator_class = "standard"`, "orchestrator_daily_warn = 0.0"} {
		if !strings.Contains(authority.BuiltInInstallationSpendPolicyBody, line+"\n") {
			t.Errorf("the built-in body does not say %q", line)
		}
	}
}

// Its keys have fixed ranges, a refusal names the key, and Set changes only the key it names.
func TestTheInstallationSpendPolicyRefusesAValueOutsideItsRange(t *testing.T) {
	base := authority.DefaultInstallationSpendPolicy()
	for _, tt := range []struct {
		key  string
		bad  []string
		good []string
	}{
		{"orchestrator_class", []string{"huge", ""}, []string{"small", "standard", "large", "frontier"}},
		{"orchestrator_daily_warn", []string{"-1", "1000001", "NaN"}, []string{"0", "25.5", "1000000"}},
	} {
		for _, value := range tt.bad {
			if _, err := base.Set(tt.key, value); !errors.Is(err, authority.ErrBadSpendPolicy) || !strings.Contains(err.Error(), tt.key) {
				t.Errorf("%s=%q reads: %v", tt.key, value, err)
			}
		}
		for _, value := range tt.good {
			if _, err := base.Set(tt.key, value); err != nil {
				t.Errorf("%s=%q: %v", tt.key, value, err)
			}
		}
	}
	got, err := base.Set("orchestrator_daily_warn", "40")
	if err != nil || got.OrchestratorDailyWarn != 40 || got.OrchestratorClass != "standard" || base.OrchestratorDailyWarn != 0 {
		t.Errorf("set reads %+v, %v; the policy it changed from reads %+v", got, err, base)
	}
	if _, err := base.Set("hard_multiplier", "3"); !errors.Is(err, authority.ErrBadSpendPolicy) {
		t.Errorf("a project key set on the installation's policy reads: %v", err)
	}
}

// A version of it keeps its body, under a kind of its own, and the session classes are the roster's.
func TestAnInstallationSpendPolicyVersionReadsBack(t *testing.T) {
	draft := authority.InstallationSpendPolicyDraft{Body: authority.BuiltInInstallationSpendPolicyBody, Policy: authority.DefaultInstallationSpendPolicy()}
	version, err := authority.NewInstallationSpendPolicyVersion("p1", "admin-1", draft, 5)
	if err != nil {
		t.Fatal(err)
	}
	if version.Kind != authority.PolicyInstallationSpend || version.Version != 1 {
		t.Errorf("version %+v", version)
	}
	got, err := authority.ReadInstallationSpendPolicy(version)
	if err != nil || got.OrchestratorClass != "standard" {
		t.Errorf("read %+v, %v", got, err)
	}
	version.Body += "# changed\n"
	if _, err := authority.ReadInstallationSpendPolicy(version); !errors.Is(err, authority.ErrPolicyDigest) {
		t.Errorf("a changed body reads: %v", err)
	}
	stored := authority.InstallationSpendPolicy{Version: 4, OrchestratorClass: "standard", OrchestratorDailyWarn: -1}
	version, err = authority.NewInstallationSpendPolicyVersion("p4", "admin-1", authority.InstallationSpendPolicyDraft{Body: draft.Body, Policy: stored}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority.ReadInstallationSpendPolicy(version); !errors.Is(err, authority.ErrBadSpendPolicy) || !strings.Contains(err.Error(), "version 4") {
		t.Errorf("a stored version outside its range reads: %v", err)
	}
	for _, class := range roster.Classes {
		if err := (authority.InstallationSpendPolicy{Version: 1, OrchestratorClass: class}).Check(); err != nil {
			t.Errorf("the roster's class %s is refused: %v", class, err)
		}
	}
}
