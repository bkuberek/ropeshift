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
	policy := authority.SpendPolicy{
		Version: 2, MaxLiveWait: "1m", MaxToolSleep: "1h", WaitRulesOff: []string{"W1", "W4"},
		WaitRulePatterns: map[string][]string{"W3": {"kubectl rollout status"}},
	}
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
