package questions

import (
	"errors"
	"slices"
	"testing"
)

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

// TestCheckBoundUnattended holds an orchestrator a member set to unattended as CheckBound does, except that it
// decides a one-way choice: it records one as decided, and never assumes one (FR-QD-8).
func TestCheckBoundUnattended(t *testing.T) {
	valid := Decision{
		ID: "d1", Status: DecisionDecided, Rationale: "It follows.", Citations: []string{"PAY-12"}, Scope: "checkout", Reversibility: Reversible,
	}
	tests := []struct {
		name   string
		change func(*Decision)
		want   error
	}{
		{"a reversible choice", func(*Decision) {}, nil},
		{"a one-way choice it decides", func(d *Decision) { d.Reversibility = OneWay }, nil},
		{"a one-way choice it assumes", func(d *Decision) { d.Reversibility, d.Status, d.Citations = OneWay, DecisionAssumed, nil }, ErrInvalid},
		{"a one-way choice with no rationale", func(d *Decision) { d.Reversibility, d.Rationale = OneWay, "" }, ErrInvalid},
		{"a one-way choice with no citation", func(d *Decision) { d.Reversibility, d.Citations = OneWay, nil }, ErrInvalid},
		{"reversibility unstated", func(d *Decision) { d.Reversibility = "" }, ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			draft := valid
			tt.change(&draft)
			if err := draft.CheckBoundUnattended(); !errors.Is(err, tt.want) {
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

// TestOnlyPlanApprovalNamesADecisionUnderItsPrefix holds the prefix of the IDs plan approval derives for the
// drafts of a plan to plan approval: no writer names a new decision under it, so nobody records one under an
// ID a draft will derive.
func TestOnlyPlanApprovalNamesADecisionUnderItsPrefix(t *testing.T) {
	draft := Decision{Status: DecisionDecided, Statement: "Retry twice."}
	for _, tt := range []struct {
		id   string
		want error
	}{
		{"pd-0123456789abcdef0123456789", ErrInvalid},
		{"pd-", ErrInvalid},
		{"pd-anything", ErrInvalid},
		{"d1", nil},
		{"pdq-1", nil},
		{"d-pd-1", nil},
	} {
		draft.ID = tt.id
		if _, err := NewDecision(draft); !errors.Is(err, tt.want) {
			t.Errorf("a new decision %q: %v, want %v", tt.id, err, tt.want)
		}
	}
}
