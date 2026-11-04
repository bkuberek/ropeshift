package server_test

import (
	"net/http"
	"slices"
	"testing"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/test/apitest"
)

func TestQuestionsAreRecordsMembersFind(t *testing.T) {
	daemon := apitest.Start(t)
	ctx := t.Context()
	first, err := daemon.Client.RecordQuestion(ctx, api.RecordQuestion{
		ID: "q1", Text: "What is the retry budget?", Categories: []string{questions.CategoryProduct}, SubjectID: "alpha",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != questions.StatusOpen {
		t.Errorf("question %+v", first)
	}
	if _, err := daemon.Client.RecordQuestion(ctx, api.RecordQuestion{ID: "q2", Text: "Where does audit data live?", Categories: []string{questions.CategoryData}}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		filter api.RecordFilter
		want   []string
	}{
		{"no filter", api.RecordFilter{}, []string{"q1", "q2"}},
		{"category", api.RecordFilter{Category: questions.CategoryData}, []string{"q2"}},
		{"status", api.RecordFilter{Status: "dismissed"}, nil},
		{"scope", api.RecordFilter{Scope: "alpha"}, []string{"q1"}},
		{"text", api.RecordFilter{Text: "retry budget"}, []string{"q1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := daemon.Client.Questions(ctx, tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, row := range rows {
				got = append(got, row.ID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v", got)
			}
		})
	}
	read, err := daemon.Client.Question(ctx, "q1")
	if err != nil || read.Text != first.Text {
		t.Errorf("read %+v: %v", read, err)
	}
	_, err = daemon.Client.Question(ctx, "absent")
	wantRefused(t, err, http.StatusNotFound, api.CodeNotFound)
	_, err = daemon.Client.RecordQuestion(ctx, api.RecordQuestion{ID: "q3", Text: "A", Categories: []string{"pricing"}})
	wantRefused(t, err, http.StatusBadRequest, api.CodeInvalid)
	if err := daemon.Client.DismissQuestion(ctx, "q2"); err != nil {
		t.Fatal(err)
	}
	wantRefused(t, daemon.Client.DismissQuestion(ctx, "q2"), http.StatusBadRequest, api.CodeInvalid)
}

func TestDecisionsAndCandidates(t *testing.T) {
	daemon := apitest.Start(t)
	ctx := t.Context()
	decided, err := daemon.Client.RecordDecision(ctx, api.RecordDecision{
		ID: "d1", Status: questions.DecisionDecided, Statement: "The retry budget is three calls.", Rationale: "It follows.",
		Citations: []string{"PAY-12"}, Scope: "checkout", Reversibility: questions.Reversible, Categories: []string{questions.CategoryProduct},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decided.DecidedBy == "" {
		t.Errorf("decision %+v", decided)
	}
	rows, err := daemon.Client.Decisions(ctx, api.RecordFilter{Scope: "checkout", Text: "retry"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("decisions %+v: %v", rows, err)
	}
	read, err := daemon.Client.Decision(ctx, "d1")
	if err != nil || read.Statement != decided.Statement {
		t.Errorf("read %+v: %v", read, err)
	}
	candidates, err := daemon.Client.Candidates(ctx, "What is the retry budget?", questions.LookupKinds(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].RecordID != "d1" || candidates[0].Kind != questions.KindDecision {
		t.Errorf("candidates %+v", candidates)
	}
	_, err = daemon.Client.Candidates(ctx, "retry", nil, 0)
	wantRefused(t, err, http.StatusBadRequest, api.CodeInvalid)
}

func TestCategoriesExtend(t *testing.T) {
	daemon := apitest.Start(t)
	ctx := t.Context()
	if err := daemon.Client.AddCategory(ctx, "pricing"); err != nil {
		t.Fatal(err)
	}
	wantRefused(t, daemon.Client.AddCategory(ctx, "Not Valid"), http.StatusBadRequest, api.CodeInvalid)
	all, err := daemon.Client.Categories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append(questions.BuiltIn(), "pricing") {
		if !slices.Contains(all, name) {
			t.Errorf("%s is missing from %v", name, all)
		}
	}
}
