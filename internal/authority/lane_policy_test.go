package authority_test

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
)

// TestWhatABindingDecisionDoesToALane holds ADR-0030's lane policy: on_binding_decision answers for every
// decision that binds a lane's work, and an on_one_way_decision that a policy sets overrides it for a one-way
// decision alone.
func TestWhatABindingDecisionDoesToALane(t *testing.T) {
	tests := []struct {
		name   string
		policy authority.LanePolicy
		oneWay bool
		want   string
	}{
		{"nothing set tells", authority.LanePolicy{}, false, authority.LaneNotify},
		{"nothing set tells a one-way decision", authority.LanePolicy{}, true, authority.LaneNotify},
		{"on_binding_decision parks a reversible decision", authority.LanePolicy{OnBindingDecision: authority.LanePark}, false, authority.LanePark},
		{"on_binding_decision parks a one-way decision", authority.LanePolicy{OnBindingDecision: authority.LanePark}, true, authority.LanePark},
		{"on_one_way_decision parks a one-way decision alone", authority.LanePolicy{OnBindingDecision: authority.LaneNotify, OnOneWayDecision: authority.LanePark}, true, authority.LanePark},
		{"on_one_way_decision leaves a reversible decision to on_binding_decision", authority.LanePolicy{OnBindingDecision: authority.LaneNotify, OnOneWayDecision: authority.LanePark}, false, authority.LaneNotify},
		{"on_one_way_decision tells where on_binding_decision parks", authority.LanePolicy{OnBindingDecision: authority.LanePark, OnOneWayDecision: authority.LaneNotify}, true, authority.LaneNotify},
		{"a policy that sets only on_one_way_decision keeps its meaning", authority.LanePolicy{OnOneWayDecision: authority.LanePark}, true, authority.LanePark},
		{"and tells every other decision", authority.LanePolicy{OnOneWayDecision: authority.LanePark}, false, authority.LaneNotify},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.BindingResponse(tt.oneWay); got != tt.want {
				t.Errorf("BindingResponse(%v) = %q, want %q", tt.oneWay, got, tt.want)
			}
		})
	}
}

func TestTheBuiltInLanePolicyTellsAndLeavesOneWayDecisionsToTheBindingKey(t *testing.T) {
	policy := authority.DefaultLanePolicy()
	if err := policy.Check(); err != nil {
		t.Fatal(err)
	}
	if policy.OnBindingDecision != authority.LaneNotify || policy.OnOneWayDecision != "" {
		t.Errorf("the built-in policy is %+v", policy)
	}
}

func TestALanePolicyChecksTheBindingKeys(t *testing.T) {
	base := authority.LanePolicy{Version: 2, OnContractVersion: authority.LaneNotify}
	for name, tt := range map[string]struct {
		binding, oneWay string
		ok              bool
	}{
		"neither set":                  {"", "", true},
		"binding set":                  {authority.LanePark, "", true},
		"one-way set":                  {"", authority.LanePark, true},
		"both set":                     {authority.LaneNotify, authority.LanePark, true},
		"a binding key it never reads": {"stop", "", false},
		"a one-way key it never reads": {"", "stop", false},
	} {
		t.Run(name, func(t *testing.T) {
			policy := base
			policy.OnBindingDecision, policy.OnOneWayDecision = tt.binding, tt.oneWay
			err := policy.Check()
			if (err == nil) != tt.ok || (err != nil && !errors.Is(err, authority.ErrBadLanePolicy)) {
				t.Errorf("Check() = %v", err)
			}
		})
	}
}
