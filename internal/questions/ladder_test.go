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
		RecommendationRationale: "The audit trail is regulated data.",
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
		{"no recommendation", func(q *Question) { q.Recommendation, q.RecommendationRationale = "", "" }, []GateCheck{GateRecommendation}},
		{"a recommendation with no rationale", func(q *Question) { q.RecommendationRationale = " " }, []GateCheck{GateRationale}},
		{"one option leaves nothing to choose", func(q *Question) { q.Options = q.Options[:1] }, []GateCheck{GateOptions}},
		{
			"every failure is named",
			func(q *Question) {
				q.LadderTrace, q.EscalationReason, q.Categories, q.Options, q.Recommendation, q.RecommendationRationale = nil, "", nil, nil, "", ""
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

// TestTheUnattendedGateKeepsAOneWayChoiceThePolicyLeavesOpen is the gate an orchestrator a member set to unattended
// passes: a one-way choice reaches a member only in a category the decision policy keeps, and a conflict between
// requirements or a change of scope reaches one whatever its category (FR-QD-8).
func TestTheUnattendedGateKeepsAOneWayChoiceThePolicyLeavesOpen(t *testing.T) {
	policy := DefaultPolicy()
	tests := []struct {
		name   string
		change func(*Question)
		want   []GateCheck
	}{
		{"a one-way choice in a category the policy keeps", func(*Question) {}, nil},
		{"a one-way choice in a category the policy leaves open", func(q *Question) { q.Categories = []string{CategoryArchitecture} }, []GateCheck{GateReason}},
		{"one kept category among open ones", func(q *Question) { q.Categories = []string{CategoryArchitecture, CategorySecurity} }, nil},
		{
			"a conflict between requirements in an open category",
			func(q *Question) {
				q.Categories, q.EscalationReason = []string{CategoryArchitecture}, ReasonRequirementConflict
			}, nil,
		},
		{"a change of scope in an open category", func(q *Question) { q.Categories, q.EscalationReason = []string{CategoryProduct}, ReasonScopeChange }, nil},
		{"what the gate refuses still fails", func(q *Question) { q.Categories, q.Options = []string{CategoryArchitecture}, nil }, []GateCheck{GateReason, GateOptions}},
		// A one-way choice that names no category counts as kept: the gate asks for a category, as it does at
		// attended, and never calls the choice the orchestrator's.
		{"a one-way choice with no category", func(q *Question) { q.Categories = nil }, []GateCheck{GateCategories}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := escalatable()
			tt.change(&q)
			var got []GateCheck
			for _, failure := range policy.GateUnattended(q) {
				if failure.Detail == "" {
					t.Errorf("%s has no detail", failure.Check)
				}
				got = append(got, failure.Check)
			}
			slices.Sort(got)
			want := slices.Clone(tt.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("failed %v, want %v", got, want)
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

// TestAOneWayChoiceWithNoCategoryIsKept holds a one-way choice that names no category for a member, whatever the
// policy leaves open, while a reversible choice with no category stays the orchestrator's.
func TestAOneWayChoiceWithNoCategoryIsKept(t *testing.T) {
	policy := DefaultPolicy()
	if !policy.KeepsOneWay(nil) {
		t.Error("an orchestrator may decide a one-way choice that names no category")
	}
	if !policy.KeepsOneWay([]string{CategoryArchitecture, CategoryData}) {
		t.Error("an orchestrator may decide a one-way choice in a category the policy keeps")
	}
	if policy.KeepsOneWay([]string{CategoryArchitecture}) {
		t.Error("the policy keeps a one-way choice in a category it leaves open")
	}
	if !policy.MayDecide(Question{}) {
		t.Error("an orchestrator may not decide a reversible choice that names no category")
	}
}

// TestAVersionStoredWithoutAKeptCategoryStillKeepsIt reads a version published before a policy had to keep security,
// compliance, and data as keeping them, so the one-way acknowledgement's promise holds with no migration of it.
func TestAVersionStoredWithoutAKeptCategoryStillKeepsIt(t *testing.T) {
	policy := Policy{OneWayCategories: []string{CategoryProduct}}
	for _, category := range []string{CategorySecurity, CategoryCompliance, CategoryData} {
		if policy.MayDecide(Question{Categories: []string{category}}) {
			t.Errorf("a stored version that dropped %s leaves it open", category)
		}
	}
	if !policy.MayDecide(Question{Categories: []string{CategoryArchitecture}}) {
		t.Error("a stored version keeps a category it never named")
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
