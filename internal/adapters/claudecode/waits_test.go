package claudecode

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// FR-SPEND-2: the wait rules a generation starts with are compiled into the policy file its hook reads, so a
// denial needs no worker. A session started with none gets no wait rules.
func TestAGenerationsPolicyCarriesItsWaitRules(t *testing.T) {
	spec := policySpec(t)
	if got := compilePolicy(spec).Waits; got != nil {
		t.Errorf("a session with no wait rules compiled %+v", got)
	}
	spec.Waits = lanes.WaitRules{Version: lanes.WaitRulesVersion, MaxToolSleepMs: 90_000, Off: []string{"W4"}, Patterns: map[string][]string{"W3": {"kubectl rollout status"}}}
	got := compilePolicy(spec).Waits
	if got == nil || got.MaxToolSleepMs != 90_000 || !slices.Equal(got.Off, []string{"W4"}) || got.Patterns["W3"][0] != "kubectl rollout status" {
		t.Fatalf("the policy carries %+v", got)
	}
	policy := compilePolicy(spec)
	if _, rule, denied := policy.DecideRule(lanes.ToolCall{Tool: lanes.ToolBash, Command: "kubectl rollout status deploy/x", Cwd: spec.Dir}); !denied || rule != "W3" {
		t.Errorf("the compiled policy denies rule %q, %v", rule, denied)
	}
}
