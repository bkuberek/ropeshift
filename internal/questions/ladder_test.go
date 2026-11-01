package questions

import (
	"slices"
	"testing"
)

func escalatable() Question {
	return Question{
		ID: "q", Text: "May the audit data leave the region?", Status: StatusTriage, Rung: RungEscalate,
		Categories: []string{CategoryData}, EscalationReason: ReasonOneWay,
		Options: []string{"Keep it in the region", "Allow one replica"}, Recommendation: "Keep it in the region",
		LadderTrace: []Step{
			{Rung: RungLookup, Candidates: []string{"decision:d1"}, Outcome: "no match"},
			{Rung: RungOpen, Outcome: "no match"},
		},
	}
}

// TestGateRefusesWhatIsMissing is the gate of FR-QD-3, one row per thing it requires.
func TestGateRefusesWhatIsMissing(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Question)
		want   []GateCheck
	}{
		{"everything is there", func(*Question) {}, nil},
		{"no ladder trace", func(q *Question) { q.LadderTrace = nil }, []GateCheck{GateTrace}},
		{"rung 1 did not run", func(q *Question) { q.LadderTrace = q.LadderTrace[1:] }, []GateCheck{GateTrace}},
		{"rung 2 did not run", func(q *Question) { q.LadderTrace = q.LadderTrace[:1] }, []GateCheck{GateTrace}},
		{"no reason", func(q *Question) { q.EscalationReason = "" }, []GateCheck{GateReason}},
		{"a reason the design does not name", func(q *Question) { q.EscalationReason = "hard" }, []GateCheck{GateReason}},
		{"no categories", func(q *Question) { q.Categories = nil }, []GateCheck{GateCategories}},
		{"no options", func(q *Question) { q.Options = nil }, []GateCheck{GateOptions}},
		{"no recommendation", func(q *Question) { q.Recommendation = "" }, []GateCheck{GateRecommendation}},
		{"one option leaves nothing to choose", func(q *Question) { q.Options = q.Options[:1] }, []GateCheck{GateOptions}},
		{
			"every failure is named",
			func(q *Question) {
				q.LadderTrace, q.EscalationReason, q.Categories, q.Options, q.Recommendation = nil, "", nil, nil, ""
			},
			[]GateCheck{GateTrace, GateReason, GateCategories, GateOptions, GateRecommendation},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := escalatable()
			tt.change(&q)
			var got []GateCheck
			for _, failure := range Gate(q) {
				if failure.Detail == "" {
					t.Errorf("%s has no detail", failure.Check)
				}
				got = append(got, failure.Check)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("failed %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAOneWayCategorySkipsRungsThreeAndFour keeps rungs 1 and 2 for it, because an earlier
// human decision may already answer it.
func TestAOneWayCategorySkipsRungsThreeAndFour(t *testing.T) {
	policy := Policy{OneWayCategories: []string{CategorySecurity, CategoryCompliance}}
	tests := []struct {
		name       string
		categories []string
		rung       int64
		want       int64
	}{
		{"rung 1 runs for a one-way category", []string{CategorySecurity}, RungLookup, RungOpen},
		{"after rung 2, a one-way category goes to a human", []string{CategorySecurity}, RungOpen, RungEscalate},
		{"one one-way category among others is enough", []string{CategoryProduct, CategoryCompliance}, RungOpen, RungEscalate},
		{"any other question goes on to rung 3", []string{CategoryProduct}, RungOpen, RungDecide},
		{"rung 3 goes on to rung 4", []string{CategoryProduct}, RungDecide, RungAssume},
		{"rung 4 goes on to rung 5", []string{CategoryProduct}, RungAssume, RungEscalate},
		{"rung 5 is the last", []string{CategoryProduct}, RungEscalate, RungEscalate},
		{"no category is not one-way", nil, RungOpen, RungDecide},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := policy.NextRung(Question{Categories: tt.categories, Rung: tt.rung}); got != tt.want {
				t.Errorf("rung %d, want %d", got, tt.want)
			}
		})
	}
}

func TestOnlyAnOrchestratorsRungsAreClosedToAOneWayCategory(t *testing.T) {
	policy := Policy{OneWayCategories: []string{CategorySecurity}}
	if policy.MayDecide(Question{Categories: []string{CategorySecurity}}) {
		t.Error("an orchestrator may decide a one-way category")
	}
	if !policy.MayDecide(Question{Categories: []string{CategoryProduct}}) {
		t.Error("an orchestrator may not decide a reversible category")
	}
}
