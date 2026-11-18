package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
)

// `spend show` prints each key with its value and version, for the project and for the installation, and the spend of
// each open work order against its limits.
func TestSpendShowPrintsEachKeyWithItsVersion(t *testing.T) {
	s := newSession(t)
	s.create("wo")
	text := s.must("spend", "show")
	for _, want := range []string{
		"spend policy\tversion 1\tbuilt-in", "hard_multiplier\t2", "no_history_limit.medium\t25", "wait_rules_off\t\n",
		"installation spend policy\tversion 1", "orchestrator_class\tstandard", "wo\tchange\t$0.00\t$12.50\t$25.00",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("spend show leaves out %q:\n%s", want, text)
		}
	}
	var view api.SpendView
	if err := json.Unmarshal([]byte(s.must("spend", "show", "--json")), &view); err != nil || view.Project.Version != 1 || len(view.Project.Keys) != len(authority.SpendPolicyKeys) {
		t.Errorf("spend show --json reads %+v: %v", view, err)
	}
	if bare := s.must("spend"); bare != text {
		t.Errorf("spend alone prints %q, and spend show %q", bare, text)
	}
}

// `spend set` publishes a new version from the one in force with only the keys named changed, and the limits follow it
// at once. A revert records an earlier version as the newest, and the earlier limits are back.
func TestSpendSetChangesOnlyTheNamedKeysAndARevertRestoresThem(t *testing.T) {
	s := newSession(t)
	s.create("wo")
	if text := s.must("spend", "set", "no_history_limit.medium=40", "hard_multiplier=3"); !strings.HasPrefix(text, "2\tmember") {
		t.Fatalf("spend set printed %q", text)
	}
	var view api.SpendView
	if err := json.Unmarshal([]byte(s.must("spend", "show", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	if view.Project.Version != 2 {
		t.Fatalf("the version in force is %d", view.Project.Version)
	}
	changed := map[string]string{"no_history_limit.medium": "40", "hard_multiplier": "3"}
	for _, key := range view.Project.Keys {
		want := authority.DefaultSpendPolicy().Text(key.Key)
		if value, ok := changed[key.Key]; ok {
			want = value
		}
		if key.Value != want {
			t.Errorf("%s reads %q, want %q", key.Key, key.Value, want)
		}
	}
	if len(view.WorkOrders) != 1 || view.WorkOrders[0].HardLimit != 40 || view.WorkOrders[0].PolicyVersion != 2 {
		t.Errorf("the work order's limits read %+v", view.WorkOrders)
	}
	if text := s.must("spend", "set", "hard_multiplier=3"); !strings.Contains(text, "unchanged") {
		t.Errorf("a set that changes nothing printed %q", text)
	}
	if text := s.must("spend", "revert", "1"); !strings.HasPrefix(text, "3\tmember") {
		t.Fatalf("spend revert printed %q", text)
	}
	if err := json.Unmarshal([]byte(s.must("spend", "show", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	if view.Project.Version != 3 || len(view.WorkOrders) != 1 || view.WorkOrders[0].HardLimit != 25 {
		t.Errorf("after the revert the project reads version %d with %+v", view.Project.Version, view.WorkOrders)
	}
	for _, key := range view.Project.Keys {
		if want := authority.DefaultSpendPolicy().Text(key.Key); key.Value != want {
			t.Errorf("after the revert %s reads %q, want %q", key.Key, key.Value, want)
		}
	}
	history := s.must("policy", "list", "--kind", "spend")
	if strings.Count(history, "\n") != 3 {
		t.Errorf("the history holds %q", history)
	}
}

// A key outside its range, an unknown key, and a call that names nothing are refused, and change nothing.
func TestSpendSetRefusesWhatItCannotPublish(t *testing.T) {
	s := newSession(t)
	for name, args := range map[string][]string{
		"a multiplier past its range":      {"spend", "set", "hard_multiplier=11"},
		"an unknown key":                   {"spend", "set", "hard_multiplers=3"},
		"no equals sign":                   {"spend", "set", "hard_multiplier"},
		"nothing named":                    {"spend", "set"},
		"a project key as installation":    {"spend", "set", "--installation", "hard_multiplier=3"},
		"a revert to no version":           {"spend", "revert", "9"},
		"a revert to the version in force": {"spend", "revert", "1"},
	} {
		t.Run(name, func(t *testing.T) {
			code, _, stderr := s.run(args...)
			if code == cli.ExitOK || stderr == "" {
				t.Errorf("%v: exit %d, %q", args, code, stderr)
			}
		})
	}
	for want, args := range map[int][]string{
		cli.ExitUsage:    {"spend", "set", "hard_multiplier=11"},
		cli.ExitNotFound: {"spend", "revert", "9"},
	} {
		if code, _, _ := s.run(args...); code != want {
			t.Errorf("%v exits %d, want %d", args, code, want)
		}
	}
	if code, _, _ := s.run("spend", "revert", "1"); code != cli.ExitUsage {
		t.Errorf("a revert to the version in force exits %d", code)
	}
	if text := s.must("policy", "list", "--kind", "spend"); strings.Count(text, "\n") != 1 {
		t.Errorf("a refused call left the history at %q", text)
	}
}

// `spend set --installation` changes the installation's two keys the same way, as a version of its own.
func TestSpendSetInstallationChangesTheInstallationsPolicy(t *testing.T) {
	s := newSession(t)
	if text := s.must("spend", "set", "--installation", "orchestrator_daily_warn=40", "orchestrator_class=small"); !strings.HasPrefix(text, "2\tmember") {
		t.Fatalf("spend set --installation printed %q", text)
	}
	var view api.SpendView
	if err := json.Unmarshal([]byte(s.must("spend", "show", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	if view.Installation.Version != 2 || view.Project.Version != 1 {
		t.Errorf("the versions read %d and %d", view.Installation.Version, view.Project.Version)
	}
	values := map[string]string{}
	for _, key := range view.Installation.Keys {
		values[key.Key] = key.Value
	}
	if values["orchestrator_daily_warn"] != "40" || values["orchestrator_class"] != "small" {
		t.Errorf("the installation reads %v", values)
	}
	if text := s.must("spend", "revert", "--installation", "1"); !strings.HasPrefix(text, "3\tmember") {
		t.Errorf("the installation's revert printed %q", text)
	}
	if text := s.must("policy", "list", "--kind", "installation_spend"); strings.Count(text, "\n") != 3 {
		t.Errorf("the installation's history holds %q", text)
	}
}

// `spend --work-order` prints a work order's charged spend, labeled, beside its limits, and --json gives the same
// figures.
func TestSpendWorkOrderPrintsChargedSpendAgainstItsLimits(t *testing.T) {
	s := newSession(t)
	s.create("wo")
	text := s.must("spend", "--work-order", "wo")
	for _, want := range []string{"work order wo", "charged\t$0.00", "estimated, at list price, with 0 turns estimated by average", "soft limit\t$12.50", "hard limit\t$25.00", "basis\tno_history"} {
		if !strings.Contains(text, want) {
			t.Errorf("spend --work-order leaves out %q:\n%s", want, text)
		}
	}
	var spend ledger.WorkOrderSpend
	if err := json.Unmarshal([]byte(s.must("spend", "--work-order", "wo", "--json")), &spend); err != nil || spend.WorkOrder != "wo" || spend.HardLimit != 25 || spend.Label == "" {
		t.Errorf("spend --work-order --json reads %+v: %v", spend, err)
	}
	if code, _, stderr := s.run("spend", "--work-order", "nope"); code != cli.ExitNotFound || stderr == "" {
		t.Errorf("a work order nobody made: exit %d, %q", code, stderr)
	}
}
