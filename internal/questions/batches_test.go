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
	all := questions.Present(batch, rows, "")
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
	if only := questions.Present(batch, rows, "data"); len(only.Items) != 1 || only.Items[0].ID != "q1" {
		t.Fatalf("the data category shows %+v", only.Items)
	}
	if none := questions.Present(batch, rows, "business"); len(none.Items) != 0 || none.Items == nil {
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
	for _, item := range questions.Present(questions.Batch{ID: "b1"}, rows, "").Items {
		scopes = append(scopes, item.Scope)
	}
	if want := []string{"plan:p1", "wo:w1", ""}; !slices.Equal(scopes, want) {
		t.Errorf("the items bind %q, want %q", scopes, want)
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
