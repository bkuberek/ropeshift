package authority_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

// The keys a new version changed are named with their values before and after, in the order `spend show` lists them, and
// a key the version left as it was is not (FR-SPEND-19).
func TestASpendPolicyNamesTheKeysAVersionChanged(t *testing.T) {
	before := authority.DefaultSpendPolicy()
	after, err := before.Set("project_daily_limit", "80")
	if err != nil {
		t.Fatal(err)
	}
	if after, err = after.Set("hard_multiplier", "3"); err != nil {
		t.Fatal(err)
	}
	if after, err = after.Set("wait_rule_patterns.W3", "kubectl rollout status"); err != nil {
		t.Fatal(err)
	}
	want := []ledger.SpendKeyChange{
		{Key: "wait_rule_patterns.W3", Before: "", After: "kubectl rollout status"},
		{Key: "hard_multiplier", Before: "2", After: "3"},
		{Key: "project_daily_limit", Before: "0", After: "80"},
	}
	got := before.ChangesTo(after)
	if len(got) != len(want) {
		t.Fatalf("changes %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("change %d is %+v, want %+v", i, got[i], want[i])
		}
	}
	if same := before.ChangesTo(before); len(same) != 0 {
		t.Errorf("a version that changed nothing names %+v", same)
	}
}

// The installation's two keys are named the same way.
func TestAnInstallationSpendPolicyNamesTheKeysAVersionChanged(t *testing.T) {
	before := authority.DefaultInstallationSpendPolicy()
	after, err := before.Set("orchestrator_daily_warn", "40")
	if err != nil {
		t.Fatal(err)
	}
	got := before.ChangesTo(after)
	if len(got) != 1 || got[0] != (ledger.SpendKeyChange{Key: "orchestrator_daily_warn", Before: "0", After: "40"}) {
		t.Errorf("changes %+v", got)
	}
}
