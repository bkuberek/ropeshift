package lanes_test

import (
	"testing"

	"ropeshift.dev/ropeshift/internal/lanes"
)

func TestABaselineWithNoRoomIsOversized(t *testing.T) {
	policy := lanes.ContextPolicy{RoleThresholds: lanes.RoleThresholds{HandoffTokens: 180000}, MinHeadroom: 40000}
	tests := []struct {
		name     string
		policy   lanes.ContextPolicy
		baseline int64
		want     bool
	}{
		{"plenty of room", policy, 30000, false},
		{"exactly the headroom", policy, 140000, false},
		{"one token past it", policy, 140001, true},
		{"past the handoff itself", policy, 190000, true},
		{"nothing measured yet", policy, 0, false},
		{"no handoff to protect", lanes.ContextPolicy{MinHeadroom: 40000}, 190000, false},
		{"no headroom asked for", lanes.ContextPolicy{RoleThresholds: lanes.RoleThresholds{HandoffTokens: 180000}}, 179000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.Oversized(tt.baseline); got != tt.want {
				t.Errorf("oversized %v", got)
			}
		})
	}
}

func TestAPolicyReadsItsRole(t *testing.T) {
	settings := lanes.ContextSettings{
		MinHeadroomTokens: 7,
		Roles:             map[string]lanes.RoleThresholds{lanes.RoleWriter: {WarnTokens: 1, CompactTokens: 2, HandoffTokens: 3}},
	}
	got := lanes.PolicyFor(settings, lanes.RoleWriter)
	if got.WarnTokens != 1 || got.CompactTokens != 2 || got.HandoffTokens != 3 || got.MinHeadroom != 7 {
		t.Errorf("policy %+v", got)
	}
	if none := lanes.PolicyFor(settings, "reviewer"); none.HandoffTokens != 0 || none.MinHeadroom != 7 {
		t.Errorf("a role with no thresholds reads %+v", none)
	}
}
