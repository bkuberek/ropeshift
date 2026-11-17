package authority_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
)

// The built-in spend policy carries the values the design states as defaults, and its body says each of them.
func TestTheBuiltInSpendPolicyIsTheOneTheDesignStates(t *testing.T) {
	got := authority.DefaultSpendPolicy()
	if got.Version != 1 || got.MaxLiveWait != "5m" || got.MaxToolSleep != "60s" || len(got.WaitRulesOff) != 0 {
		t.Errorf("the built-in policy reads %+v", got)
	}
	for _, rule := range []string{"W1", "W2", "W3", "W4"} {
		if patterns, ok := got.WaitRulePatterns[rule]; !ok || len(patterns) != 0 {
			t.Errorf("rule %s patterns %v, %v", rule, patterns, ok)
		}
	}
	if err := got.Check(); err != nil {
		t.Errorf("the built-in policy does not check: %v", err)
	}
	for _, line := range []string{"[spend_policy]", "version = 1", `max_live_wait = "5m"`, `max_tool_sleep = "60s"`, "wait_rules_off = []"} {
		if !strings.Contains(authority.BuiltInSpendPolicyBody, line+"\n") {
			t.Errorf("the built-in body does not say %q", line)
		}
	}
	if live, sleep := got.LiveWait(), got.ToolSleep(); live != 5*time.Minute || sleep != time.Minute {
		t.Errorf("the durations read %s and %s", live, sleep)
	}
}

// Every key the design names is there with its stated default, and the built-in body states every one of them,
// so a version always says what it runs on.
func TestTheBuiltInSpendPolicyStatesEveryKeyWithItsDefault(t *testing.T) {
	defaults := map[string]string{
		"max_live_wait": "5m", "max_tool_sleep": "60s", "wait_rules_off": "",
		"wait_rule_patterns.W1": "", "wait_rule_patterns.W2": "", "wait_rule_patterns.W3": "", "wait_rule_patterns.W4": "",
		"soft_quantile": "p85", "hard_multiplier": "2", "low_confidence_outcomes": "6", "low_confidence_soft_share": "0.5",
		"no_history_limit.small": "10", "no_history_limit.medium": "25", "no_history_limit.large": "60",
		"review_share": "0.5", "review_soft_share": "0.5", "plan_hard_multiplier": "2",
		"project_daily_limit": "0", "project_weekly_limit": "0", "project_warn_share": "0.8", "assistant_daily_warn": "0",
		"budget_flag_headroom_share": "0.1", "repeat_limit": "3", "repeat_denials_to_park": "2", "no_progress_share": "0.5",
		"no_progress_park_multiplier": "2", "no_history_no_progress": "5", "waiting_alert_share": "0.5", "waiting_alert_window": "30m",
		"waiting_alert_min_calls": "5", "assistant_class": "standard",
	}
	policy := authority.DefaultSpendPolicy()
	if len(authority.SpendPolicyKeys) != len(defaults) {
		t.Errorf("the policy lists %d keys, and the design states %d", len(authority.SpendPolicyKeys), len(defaults))
	}
	for _, key := range authority.SpendPolicyKeys {
		want, ok := defaults[key]
		if !ok {
			t.Errorf("%s is no key of the design", key)
			continue
		}
		if got := policy.Text(key); got != want {
			t.Errorf("%s defaults to %q, want %q", key, got, want)
		}
	}
	if got := authority.DefaultSpendPolicy().Body(); got != authority.BuiltInSpendPolicyBody {
		t.Errorf("the built-in body is not the default policy stated:\n%s", got)
	}
	for _, line := range []string{
		`soft_quantile = "p85"`, "hard_multiplier = 2.0", "low_confidence_outcomes = 6", "no_history_limit = { small = 10.0, medium = 25.0, large = 60.0 }",
		"project_daily_limit = 0.0", "repeat_limit = 3", `waiting_alert_window = "30m"`, `assistant_class = "standard"`,
		"wait_rule_patterns = { W1 = [], W2 = [], W3 = [], W4 = [] }",
	} {
		if !strings.Contains(authority.BuiltInSpendPolicyBody, line+"\n") {
			t.Errorf("the built-in body does not say %q", line)
		}
	}
}

// Each key has a fixed range, and a value outside it is refused with the key named. The edges are allowed.
func TestEachSpendKeyHasItsRange(t *testing.T) {
	tests := []struct {
		key  string
		bad  []string
		good []string
	}{
		{"max_live_wait", []string{"30s", "2h", "soon"}, []string{"1m", "1h"}},
		{"max_tool_sleep", []string{"0s", "90m"}, []string{"1s", "1h"}},
		{"soft_quantile", []string{"p99", "median"}, []string{"p50", "p85"}},
		{"hard_multiplier", []string{"0.9", "11", "-2", "NaN"}, []string{"1", "3", "10"}},
		{"low_confidence_outcomes", []string{"0", "101", "1.5"}, []string{"1", "100"}},
		{"low_confidence_soft_share", []string{"-0.1", "1.1"}, []string{"0", "1"}},
		{"no_history_limit.small", []string{"-1", "1000001"}, []string{"0", "12.5", "1000000"}},
		{"no_history_limit.medium", []string{"-1"}, []string{"30"}},
		{"no_history_limit.large", []string{"-0.01"}, []string{"80"}},
		{"review_share", []string{"-0.5", "2"}, []string{"0", "1"}},
		{"review_soft_share", []string{"1.5"}, []string{"0.25"}},
		{"plan_hard_multiplier", []string{"0.5", "12"}, []string{"1", "10"}},
		{"project_daily_limit", []string{"-5"}, []string{"0", "200"}},
		{"project_weekly_limit", []string{"-5"}, []string{"0", "900"}},
		{"project_warn_share", []string{"-1", "1.2"}, []string{"0.8"}},
		{"assistant_daily_warn", []string{"-1"}, []string{"3"}},
		{"budget_flag_headroom_share", []string{"-0.1", "1.1"}, []string{"0", "0.25"}},
		{"repeat_limit", []string{"1", "21", "x"}, []string{"2", "20"}},
		{"repeat_denials_to_park", []string{"0", "11"}, []string{"1", "10"}},
		{"no_progress_share", []string{"1.1"}, []string{"0.4"}},
		{"no_progress_park_multiplier", []string{"0.5", "11"}, []string{"1", "10"}},
		{"no_history_no_progress", []string{"-1"}, []string{"0", "8"}},
		{"waiting_alert_share", []string{"-0.2", "2"}, []string{"0.7"}},
		{"waiting_alert_window", []string{"30s", "25h"}, []string{"1m", "24h"}},
		{"waiting_alert_min_calls", []string{"0", "1001"}, []string{"1", "1000"}},
		{"assistant_class", []string{"huge", ""}, []string{"small", "standard", "large", "frontier"}},
		{"wait_rules_off", []string{"W5", "W1,w2"}, []string{"", "W1,W4"}},
		{"wait_rule_patterns.W3", []string{strings.Repeat("a", 300)}, []string{"kubectl rollout status", "a b, c d"}},
	}
	base := authority.DefaultSpendPolicy()
	base.Version = 2
	for _, tt := range tests {
		for _, value := range tt.bad {
			t.Run(tt.key+"="+value, func(t *testing.T) {
				_, err := base.Set(tt.key, value)
				if !errors.Is(err, authority.ErrBadSpendPolicy) || !strings.Contains(err.Error(), strings.SplitN(tt.key, ".", 2)[0]) {
					t.Errorf("%s=%q reads: %v", tt.key, value, err)
				}
			})
		}
		for _, value := range tt.good {
			t.Run(tt.key+"="+value+" is allowed", func(t *testing.T) {
				got, err := base.Set(tt.key, value)
				if err != nil {
					t.Fatalf("%s=%q: %v", tt.key, value, err)
				}
				if err := got.Check(); err != nil {
					t.Errorf("%s=%q does not check: %v", tt.key, value, err)
				}
			})
		}
	}
}

// Set changes the one key it names and leaves every other as it was, and a key nobody offers is refused.
func TestSetChangesOnlyTheKeyItNames(t *testing.T) {
	base := authority.DefaultSpendPolicy()
	got, err := base.Set("hard_multiplier", "3")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range authority.SpendPolicyKeys {
		want := base.Text(key)
		if key == "hard_multiplier" {
			want = "3"
		}
		if got.Text(key) != want {
			t.Errorf("%s reads %q after the change, want %q", key, got.Text(key), want)
		}
	}
	if base.Text("hard_multiplier") != "2" {
		t.Errorf("the policy it changed from reads %q", base.Text("hard_multiplier"))
	}
	patterned, err := base.Set("wait_rule_patterns.W3", "kubectl rollout status, helm status")
	if err != nil || !slices.Equal(patterned.WaitRulePatterns["W3"], []string{"kubectl rollout status", "helm status"}) || len(base.WaitRulePatterns["W3"]) != 0 {
		t.Errorf("patterns read %v, %v; the policy changed from reads %v", patterned.WaitRulePatterns, err, base.WaitRulePatterns)
	}
	for _, key := range []string{"hard_multiplier_x", "no_history_limit", "no_history_limit.huge", "wait_rule_patterns", "wait_rule_patterns.W9", "version"} {
		if _, err := base.Set(key, "1"); !errors.Is(err, authority.ErrBadSpendPolicy) {
			t.Errorf("%s reads: %v", key, err)
		}
	}
}

// The body a policy states reads back as the same policy once a version is made of it.
func TestASpendPolicyBodyStatesEveryKeyOfThePolicy(t *testing.T) {
	policy, err := authority.DefaultSpendPolicy().Set("no_history_limit.medium", "40")
	if err != nil {
		t.Fatal(err)
	}
	policy.Version = 3
	policy.WaitRulesOff = []string{"W4"}
	policy.WaitRulePatterns = map[string][]string{"W3": {"kubectl rollout status"}}
	body := policy.Body()
	for _, line := range []string{
		"version = 3", `wait_rules_off = ["W4"]`, `wait_rule_patterns = { W1 = [], W2 = [], W3 = ["kubectl rollout status"], W4 = [] }`,
		"no_history_limit = { small = 10.0, medium = 40.0, large = 60.0 }",
	} {
		if !strings.Contains(body, line+"\n") {
			t.Errorf("the body does not say %q:\n%s", line, body)
		}
	}
}

// A value outside its key's range is refused, and the refusal names the key.
func TestASpendPolicyRefusesAValueOutsideItsRange(t *testing.T) {
	good := authority.DefaultSpendPolicy()
	good.Version = 2
	tests := []struct {
		name   string
		change func(*authority.SpendPolicy)
		key    string
	}{
		{"no version", func(p *authority.SpendPolicy) { p.Version = 0 }, "version"},
		{"a live wait under a minute", func(p *authority.SpendPolicy) { p.MaxLiveWait = "30s" }, "max_live_wait"},
		{"a live wait past an hour", func(p *authority.SpendPolicy) { p.MaxLiveWait = "2h" }, "max_live_wait"},
		{"a live wait that is no duration", func(p *authority.SpendPolicy) { p.MaxLiveWait = "five minutes" }, "max_live_wait"},
		{"a tool sleep of nothing", func(p *authority.SpendPolicy) { p.MaxToolSleep = "0s" }, "max_tool_sleep"},
		{"a tool sleep past an hour", func(p *authority.SpendPolicy) { p.MaxToolSleep = "90m" }, "max_tool_sleep"},
		{"a rule that does not exist", func(p *authority.SpendPolicy) { p.WaitRulesOff = []string{"W5"} }, "wait_rules_off"},
		{"patterns for a rule that does not exist", func(p *authority.SpendPolicy) { p.WaitRulePatterns = map[string][]string{"W9": {"x"}} }, "wait_rule_patterns"},
		{"an empty pattern", func(p *authority.SpendPolicy) { p.WaitRulePatterns = map[string][]string{"W3": {"  "}} }, "wait_rule_patterns"},
		{"a pattern a line long", func(p *authority.SpendPolicy) {
			p.WaitRulePatterns = map[string][]string{"W3": {strings.Repeat("a", 300)}}
		}, "wait_rule_patterns"},
		{"a thousand patterns", func(p *authority.SpendPolicy) {
			p.WaitRulePatterns = map[string][]string{"W3": slices.Repeat([]string{"a b"}, 1000)}
		}, "wait_rule_patterns"},
	}
	if err := good.Check(); err != nil {
		t.Fatalf("a good policy does not check: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := authority.DefaultSpendPolicy()
			bad.Version = 2
			tt.change(&bad)
			err := bad.Check()
			if !errors.Is(err, authority.ErrBadSpendPolicy) || !strings.Contains(err.Error(), tt.key) {
				t.Errorf("the policy checks: %v", err)
			}
		})
	}
}

// The edges of the ranges are allowed, and a member's own values read back.
func TestASpendPolicyAcceptsTheEdgesOfItsRanges(t *testing.T) {
	policy := authority.DefaultSpendPolicy()
	policy.Version, policy.MaxLiveWait, policy.MaxToolSleep, policy.WaitRulesOff = 2, "1m", "1h", []string{"W1", "W4"}
	policy.WaitRulePatterns = map[string][]string{"W3": {"kubectl rollout status"}}
	if err := policy.Check(); err != nil {
		t.Fatal(err)
	}
	policy.MaxLiveWait, policy.MaxToolSleep = "1h", "1s"
	if err := policy.Check(); err != nil {
		t.Fatal(err)
	}
	if policy.LiveWait() != time.Hour || policy.ToolSleep() != time.Second {
		t.Errorf("the durations read %s and %s", policy.LiveWait(), policy.ToolSleep())
	}
}

// A version keeps its body, and a body that no longer matches its digest reads as nothing.
func TestASpendPolicyVersionReadsBackAndRefusesAnotherBody(t *testing.T) {
	draft := authority.SpendPolicyDraft{Body: authority.BuiltInSpendPolicyBody, Policy: authority.DefaultSpendPolicy()}
	version, err := authority.NewSpendPolicyVersion("p1", "member-1", draft, 5)
	if err != nil {
		t.Fatal(err)
	}
	if version.Kind != authority.PolicySpend || version.Version != 1 {
		t.Errorf("version %+v", version)
	}
	got, err := authority.ReadSpendPolicy(version)
	if err != nil || got.MaxLiveWait != "5m" || got.Version != 1 {
		t.Errorf("read %+v, %v", got, err)
	}
	version.Body += "# changed\n"
	if _, err := authority.ReadSpendPolicy(version); !errors.Is(err, authority.ErrPolicyDigest) {
		t.Errorf("a changed body reads: %v", err)
	}
}

// The digest covers the body, and the settings are what the code reads, so a stored version whose settings sit
// outside a range is refused on read and names its version, and never reaches a limit or a wait.
func TestAStoredSpendPolicyVersionOutsideItsRangesIsRefusedOnRead(t *testing.T) {
	cases := map[string]func(*authority.SpendPolicy){
		"a multiplier past its range": func(p *authority.SpendPolicy) { p.HardMultiplier = 99 },
		"a wait that is no duration":  func(p *authority.SpendPolicy) { p.MaxLiveWait = "soon" },
		"a class that does not exist": func(p *authority.SpendPolicy) { p.AssistantClass = "huge" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			policy := authority.DefaultSpendPolicy()
			policy.Version = 3
			change(&policy)
			draft := authority.SpendPolicyDraft{Body: authority.BuiltInSpendPolicyBody, Policy: policy}
			version, err := authority.NewSpendPolicyVersion("p3", "member-1", draft, 5)
			if err != nil {
				t.Fatal(err)
			}
			_, err = authority.ReadSpendPolicy(version)
			if !errors.Is(err, authority.ErrBadSpendPolicy) || !strings.Contains(err.Error(), "version 3") {
				t.Errorf("a stored version %s reads: %v", name, err)
			}
		})
	}
}

// A policy that did not check never reads as a zero wait: a duration that does not parse reads as its default.
func TestAWaitThatDoesNotParseReadsAsItsDefaultAndNeverAsZero(t *testing.T) {
	policy := authority.DefaultSpendPolicy()
	policy.MaxLiveWait, policy.MaxToolSleep, policy.WaitingAlertWindow = "soon", "", "later"
	if got := policy.LiveWait(); got != 5*time.Minute {
		t.Errorf("a live wait that is no duration reads %s", got)
	}
	if got := policy.ToolSleep(); got != time.Minute {
		t.Errorf("a tool sleep that is no duration reads %s", got)
	}
	if got := policy.AlertWindow(); got != 30*time.Minute {
		t.Errorf("an alert window that is no duration reads %s", got)
	}
}
