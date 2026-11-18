package questions_test

import (
	"errors"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

func TestBatchPolicyFillsWhatIsNotStated(t *testing.T) {
	tests := []struct {
		name string
		in   questions.BatchPolicy
		want questions.BatchPolicy
	}{
		{"nothing stated", questions.BatchPolicy{}, questions.BatchPolicy{WindowMs: questions.DefaultBatchWindowMs, MaxWaitMs: questions.DefaultBatchMaxWaitMs}},
		{"both stated", questions.BatchPolicy{WindowMs: 7, MaxWaitMs: 3}, questions.BatchPolicy{WindowMs: 7, MaxWaitMs: 3}},
		{"a negative limit is no limit", questions.BatchPolicy{WindowMs: -1, MaxWaitMs: 3}, questions.BatchPolicy{WindowMs: questions.DefaultBatchWindowMs, MaxWaitMs: 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.Filled(); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPresentShowsWhatWaitsForAMember(t *testing.T) {
	rows := []questions.Question{
		{
			ID: "q1", Text: "Keep the budget?", Status: questions.StatusNeedsHuman, Categories: []string{"product", "data"}, Blocking: true,
			Options: []string{"Keep", "Change"}, Recommendation: "Keep", EscalationReason: questions.ReasonOneWay,
			LadderTrace: []questions.Step{
				{Rung: questions.RungLookup, Candidates: []string{"d1", "r1"}},
				{Rung: questions.RungOpen, Candidates: []string{"d1", "q9"}},
			},
		},
		{ID: "q2", Text: "Answered?", Status: questions.StatusAnswered, Categories: []string{"product"}},
		{ID: "q3", Text: "Dismissed?", Status: questions.StatusDismissed, Categories: []string{"product"}},
		{ID: "q4", Text: "Security?", Status: questions.StatusNeedsHuman, Categories: []string{"security"}},
	}
	batch := questions.Batch{ID: "b1"}
	policy := questions.DefaultPolicy()
	all := questions.Present(batch, rows, "", policy)
	if len(all.Items) != 2 || all.Items[0].ID != "q1" || all.Items[1].ID != "q4" {
		t.Fatalf("items %+v", all.Items)
	}
	first := all.Items[0]
	if !first.Blocking || first.Recommendation != "Keep" || first.EscalationReason != questions.ReasonOneWay || len(first.Options) != 2 {
		t.Fatalf("item %+v", first)
	}
	if want := []string{"d1", "r1", "q9"}; !slices.Equal(first.Citations, want) {
		t.Fatalf("citations %v, want %v once each, in the order the ladder read them", first.Citations, want)
	}
	if only := questions.Present(batch, rows, "data", policy); len(only.Items) != 1 || only.Items[0].ID != "q1" {
		t.Fatalf("the data category shows %+v", only.Items)
	}
	if none := questions.Present(batch, rows, "business", policy); len(none.Items) != 0 || none.Items == nil {
		t.Fatalf("an unused category shows %+v", none.Items)
	}
}

// TestAnItemSaysWhatItsAnswerBinds holds that an item a member answers carries the scope its answer takes: the
// record its question is about, and none for a question about nothing.
func TestAnItemSaysWhatItsAnswerBinds(t *testing.T) {
	rows := []questions.Question{
		{ID: "q1", Text: "Keep the budget?", Status: questions.StatusNeedsHuman, SubjectKind: planning.SubjectPlan, SubjectID: "p1"},
		{ID: "q2", Text: "Trim the log?", Status: questions.StatusNeedsHuman, SubjectKind: workorders.SubjectKind, SubjectID: "w1"},
		{ID: "q3", Text: "Anything else?", Status: questions.StatusNeedsHuman},
	}
	var scopes []string
	for _, item := range questions.Present(questions.Batch{ID: "b1"}, rows, "", questions.DefaultPolicy()).Items {
		scopes = append(scopes, item.Scope)
	}
	if want := []string{"plan:p1", "wo:w1", ""}; !slices.Equal(scopes, want) {
		t.Errorf("the items bind %q, want %q", scopes, want)
	}
}

// TestAnAnswerIsOneWayByReasonOrByCategory is the rule the server applies to an answer and an item states: the
// escalation reason says one-way, or the policy keeps one of the question's categories from an orchestrator.
func TestAnAnswerIsOneWayByReasonOrByCategory(t *testing.T) {
	policy := questions.DefaultPolicy()
	tests := []struct {
		name string
		q    questions.Question
		want string
	}{
		{"a one-way reason", questions.Question{EscalationReason: questions.ReasonOneWay, Categories: []string{"product"}}, questions.OneWay},
		{"a category the policy keeps", questions.Question{EscalationReason: questions.ReasonScopeChange, Categories: []string{"product", "data"}}, questions.OneWay},
		{"neither", questions.Question{EscalationReason: questions.ReasonScopeChange, Categories: []string{"product"}}, questions.Reversible},
		{"no category and a requirement conflict", questions.Question{EscalationReason: questions.ReasonRequirementConflict}, questions.Reversible},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := policy.AnswerReversibility(tt.q); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAnItemStatesTheReversibilityItsAnswerWillHave holds G6: the item says what the answer will be, whether the
// reason or the category made it so, and carries the recommendation's rationale.
func TestAnItemStatesTheReversibilityItsAnswerWillHave(t *testing.T) {
	rows := []questions.Question{
		{ID: "q1", Status: questions.StatusNeedsHuman, EscalationReason: questions.ReasonOneWay, Categories: []string{"product"}},
		{ID: "q2", Status: questions.StatusNeedsHuman, EscalationReason: questions.ReasonScopeChange, Categories: []string{"security"}},
		{
			ID: "q3", Status: questions.StatusNeedsHuman, EscalationReason: questions.ReasonScopeChange, Categories: []string{"product"},
			Recommendation: "Keep", RecommendationRationale: "It is what the brand guide shows.",
		},
	}
	items := questions.Present(questions.Batch{ID: "b1"}, rows, "", questions.DefaultPolicy()).Items
	var got []string
	for _, item := range items {
		got = append(got, item.Reversibility)
	}
	if want := []string{questions.OneWay, questions.OneWay, questions.Reversible}; !slices.Equal(got, want) {
		t.Fatalf("the items state %q, want %q", got, want)
	}
	if items[2].RecommendationRationale != "It is what the brand guide shows." {
		t.Fatalf("the item carries rationale %q", items[2].RecommendationRationale)
	}
}

func batchedQuestion() questions.Question {
	return questions.Question{
		ID: "q1", Status: questions.StatusNeedsHuman, Options: []string{"Keep", "Change"}, Recommendation: "Keep",
		RecommendationRationale: "The brand guide shows it.",
		LadderTrace:             []questions.Step{{Rung: questions.RungLookup, Candidates: []string{"decision:d1"}}},
	}
}

// TestAcceptingAnItemRecordsItsRecommendation holds FR-QD-9 for a batch: the answer is the recommendation, with
// its rationale and the item's citations, and the member's own words are refused beside it.
func TestAcceptingAnItemRecordsItsRecommendation(t *testing.T) {
	got, err := questions.BatchAnswer{Question: "q1", Accepted: true, Answer: questions.Answer{DecisionID: "d9", Supersedes: "pd-old"}}.Resolve(batchedQuestion())
	if err != nil {
		t.Fatal(err)
	}
	want := questions.Answer{
		DecisionID: "d9", Statement: "Keep", Rationale: "The brand guide shows it.", Citations: []string{"decision:d1"}, Supersedes: "pd-old",
	}
	if got.DecisionID != want.DecisionID || got.Statement != want.Statement || got.Rationale != want.Rationale ||
		!slices.Equal(got.Citations, want.Citations) || got.Supersedes != want.Supersedes {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestABatchAnswerIsRefusedWhenItStatesTooMuchOrTooLittle(t *testing.T) {
	bare := batchedQuestion()
	bare.Recommendation = ""
	tests := []struct {
		name     string
		question questions.Question
		answer   questions.BatchAnswer
	}{
		{"accepting and answering", batchedQuestion(), questions.BatchAnswer{Accepted: true, Answer: questions.Answer{Statement: "Change"}}},
		{"accepting and a rationale", batchedQuestion(), questions.BatchAnswer{Accepted: true, Answer: questions.Answer{Rationale: "Because."}}},
		{"accepting with citations", batchedQuestion(), questions.BatchAnswer{Accepted: true, Answer: questions.Answer{Citations: []string{"x"}}}},
		{"accepting what was not recommended", bare, questions.BatchAnswer{Accepted: true}},
		{"an answer that states nothing", batchedQuestion(), questions.BatchAnswer{Answer: questions.Answer{Rationale: "Because."}}},
		{"an answer with no rationale", batchedQuestion(), questions.BatchAnswer{Answer: questions.Answer{Statement: "Change"}}},
		{"an answer with a blank rationale", batchedQuestion(), questions.BatchAnswer{Answer: questions.Answer{Statement: "Change", Rationale: "  "}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.answer.Resolve(tt.question); !errors.Is(err, questions.ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
	own, err := questions.BatchAnswer{Answer: questions.Answer{DecisionID: "d1", Statement: "Change", Rationale: "It ships sooner."}}.Resolve(batchedQuestion())
	if err != nil || own.Statement != "Change" || own.Rationale != "It ships sooner." {
		t.Fatalf("a member's own answer with a rationale: %+v, %v", own, err)
	}
}

func TestAnAnswerSaysSomething(t *testing.T) {
	if err := (questions.Answer{}).Check(); !errors.Is(err, questions.ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
	if err := (questions.Answer{Statement: "Two."}).Check(); err != nil {
		t.Fatal(err)
	}
}
