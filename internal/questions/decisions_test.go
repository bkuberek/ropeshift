package questions

import (
	"errors"
	"slices"
	"testing"
)

func TestScopesOverlap(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"the same scope", "checkout", "checkout", true},
		{"one inside the other", "checkout", "checkout/retry", true},
		{"either way round", "checkout/retry", "checkout", true},
		{"a shared prefix is not a shared part", "checkout", "checkouts", false},
		{"two siblings", "checkout/retry", "checkout/refund", false},
		{"an empty scope covers nothing", "", "checkout", false},
		{"nor do two", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ScopesOverlap(tt.a, tt.b); got != tt.want {
				t.Errorf("ScopesOverlap(%q, %q) = %v", tt.a, tt.b, got)
			}
		})
	}
}

func TestUnstatedNamesWhatADraftIsSilentOn(t *testing.T) {
	draft := Decision{Supersedes: "d1", NoConflict: []string{"d2"}}
	if got := draft.Unstated([]string{"d1", "d2", "d3"}); !slices.Equal(got, []string{"d3"}) {
		t.Errorf("unstated %v", got)
	}
}

func TestCheckBound(t *testing.T) {
	valid := Decision{
		ID: "d1", Status: DecisionDecided, Rationale: "It follows.", Citations: []string{"PAY-12"}, Scope: "checkout", Reversibility: Reversible,
	}
	tests := []struct {
		name   string
		change func(*Decision)
		want   error
	}{
		{"everything stated", func(*Decision) {}, nil},
		{"a one-way choice", func(d *Decision) { d.Reversibility = OneWay }, ErrOneWay},
		{"reversibility unstated", func(d *Decision) { d.Reversibility = "" }, ErrInvalid},
		{"no rationale", func(d *Decision) { d.Rationale = "" }, ErrInvalid},
		{"no scope", func(d *Decision) { d.Scope = "" }, ErrInvalid},
		{"no citation on a decision", func(d *Decision) { d.Citations = nil }, ErrInvalid},
		{"no citation on an assumption", func(d *Decision) { d.Status, d.Citations = DecisionAssumed, nil }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			draft := valid
			tt.change(&draft)
			if err := draft.CheckBound(); !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestPolicyCheck(t *testing.T) {
	valid := DefaultPolicy()
	tests := []struct {
		name   string
		change func(*Policy)
		valid  bool
	}{
		{"the built-in policy", func(*Policy) {}, true},
		{"no assumptions at all", func(p *Policy) { p.MaxAssumptionsPerWorkOrder = 0 }, true},
		{"a category the workspace added", func(p *Policy) { p.OneWayCategories = []string{"pricing"} }, true},
		{"no version", func(p *Policy) { p.Version = 0 }, false},
		{"a default the design does not define", func(p *Policy) { p.ReversibleDefault = "ask" }, false},
		{"a limit below zero", func(p *Policy) { p.MaxAssumptionsPerWorkOrder = -1 }, false},
		{"a misspelled category", func(p *Policy) { p.OneWayCategories = []string{"secruity"} }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := valid
			tt.change(&policy)
			err := policy.Check([]string{"pricing"})
			if tt.valid && err != nil {
				t.Errorf("refused: %v", err)
			}
			if !tt.valid && !errors.Is(err, ErrInvalid) {
				t.Errorf("accepted: %v", err)
			}
		})
	}
}
