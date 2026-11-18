package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
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

// stopped gives a work order a lane that reported $30, which is past the hard limit of $25 its size gives it.
func (s *session) stopped(id string) {
	s.t.Helper()
	s.create(id)
	err := s.daemon.Store.Write(s.t.Context(), apitest.Scope, "spend.fixture", func(tx ports.WriteTx) error {
		if err := tx.Lanes().Start("lane-"+id, id, 1, ledger.LanePolicies{}); err != nil {
			return err
		}
		if err := tx.Lanes().StartGeneration(ledger.Generation{Lane: "lane-" + id, Number: 1, StartReason: ledger.StartInitial}); err != nil {
			return err
		}
		if _, err := tx.Lanes().ReportUsage("lane-"+id, 1, ledger.GenerationUsage{CostUSD: 30, KnownTurns: 3}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: ledger.Actor{Kind: ledger.ActorMember, ID: "member"}, Subject: ledger.Subject{Kind: "note", ID: id}, Data: []byte(`{}`)})
		return err
	})
	if err != nil {
		s.t.Fatal(err)
	}
}

// `spend raise` lifts a work order's hard limit by a percentage or to an amount, and prints the limit before and after,
// as --json does. `spend show` then reads the raised limit.
func TestSpendRaiseLiftsTheHardLimitAndSaysBeforeAndAfter(t *testing.T) {
	s := newSession(t)
	s.stopped("wo")
	text := s.must("spend", "raise", "wo", "--by", "100")
	for _, want := range []string{"work order wo", "raise\thard limit $25.00 -> $50.00, charged $30.00"} {
		if !strings.Contains(text, want) {
			t.Errorf("spend raise leaves out %q:\n%s", want, text)
		}
	}
	var change ledger.SpendLimitChange
	if err := json.Unmarshal([]byte(s.must("spend", "raise", "wo", "--to", "80", "--json")), &change); err != nil ||
		change.Act != ledger.SpendActRaise || change.BeforeUSD != 50 || change.AfterUSD != 80 || change.ToUSD != 80 {
		t.Errorf("spend raise --json reads %+v: %v", change, err)
	}
	if shown := s.must("spend", "--work-order", "wo"); !strings.Contains(shown, "hard limit\t$80.00") {
		t.Errorf("spend --work-order after the raises:\n%s", shown)
	}
}

// A raise that names neither a percentage nor an amount, or both, or one that does not raise the limit, and a work order
// nobody made, are refused and change nothing.
func TestSpendRaiseRefusesWhatItCannotDo(t *testing.T) {
	s := newSession(t)
	s.stopped("wo")
	for name, want := range map[string]struct {
		args []string
		exit int
	}{
		"neither":                     {[]string{"spend", "raise", "wo"}, cli.ExitUsage},
		"both":                        {[]string{"spend", "raise", "wo", "--by", "10", "--to", "90"}, cli.ExitUsage},
		"a negative share":            {[]string{"spend", "raise", "wo", "--by", "-5"}, cli.ExitUsage},
		"no work order":               {[]string{"spend", "raise", "--by", "10"}, cli.ExitUsage},
		"a limit not above":           {[]string{"spend", "raise", "wo", "--to", "25"}, cli.ExitUsage},
		"a work order nobody made":    {[]string{"spend", "raise", "nope", "--by", "10"}, cli.ExitNotFound},
		"a resume of work not parked": {[]string{"spend", "resume", "wo"}, cli.ExitUsage},
		"a resume of no work order":   {[]string{"spend", "resume", "nope"}, cli.ExitNotFound},
		"a resume with no work order": {[]string{"spend", "resume"}, cli.ExitUsage},
	} {
		t.Run(name, func(t *testing.T) {
			if code, _, stderr := s.run(want.args...); code != want.exit || stderr == "" {
				t.Errorf("%v: exit %d, %q, want %d", want.args, code, stderr, want.exit)
			}
		})
	}
	if shown := s.must("spend", "--work-order", "wo"); !strings.Contains(shown, "hard limit\t$25.00") {
		t.Errorf("a refused call moved the limit:\n%s", shown)
	}
}
