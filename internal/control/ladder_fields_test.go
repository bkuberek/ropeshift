package control_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// heldQuestion records a question and takes it through rungs 1 and 2, so an escalation is the next step.
func heldQuestion(t *testing.T) (*control.Questions, ports.Store, string, ledger.Actor) {
	t.Helper()
	ctx := t.Context()
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: "w"}, member); err != nil {
		t.Fatal(err)
	}
	ops := control.NewQuestions(store, questions.BatchPolicy{})
	lane := ledger.Actor{Kind: ledger.ActorLane, ID: "lane-a"}
	holder := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-a"}
	asked, err := ops.Ask(ctx, "w", questions.Ask{
		ID: "q-a", Text: "Which logo set should the site use?", Categories: []string{questions.CategoryProduct}, Lane: lane.ID, Generation: 1,
	}, lane)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Take(ctx, "w", asked.QuestionID, 60_000, holder); err != nil {
		t.Fatal(err)
	}
	for _, rung := range []int64{questions.RungLookup, questions.RungOpen} {
		if _, err := ops.Climb(ctx, "w", asked.QuestionID, questions.Step{Rung: rung, Outcome: "no match"}, holder); err != nil {
			t.Fatal(err)
		}
	}
	return ops, store, asked.QuestionID, holder
}

// TestEscalationTrimsItsOptions holds that an escalation stores each option and the recommendation with no
// surrounding whitespace, and drops an option that is empty once trimmed.
func TestEscalationTrimsItsOptions(t *testing.T) {
	ctx := t.Context()
	ops, _, id, holder := heldQuestion(t)
	got, err := ops.Escalate(ctx, "w", id, questions.Escalation{
		Reason: questions.ReasonRequirementConflict, Options: []string{"  Monogram\n", "", " \t ", "Wordmark "},
		Recommendation: "  Wordmark ", RecommendationRationale: "  It reads at small sizes. ",
	}, holder)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Monogram", "Wordmark"}; !slices.Equal(got.Options, want) {
		t.Errorf("options %q, want %q", got.Options, want)
	}
	if got.Recommendation != "Wordmark" || got.RecommendationRationale != "It reads at small sizes." {
		t.Errorf("recommendation %q because %q, want both trimmed", got.Recommendation, got.RecommendationRationale)
	}
}

// TestEscalationRefusesAFieldOverItsCap holds that an escalation with an oversized field or too many options is
// refused as invalid, names the field and its cap, and leaves the question held and unchanged.
func TestEscalationRefusesAFieldOverItsCap(t *testing.T) {
	long := strings.Repeat("a", 2001)
	tests := []struct {
		name   string
		change func(*questions.Escalation)
		want   string
	}{
		{"an option", func(e *questions.Escalation) { e.Options = []string{"Monogram", long} }, "option 2"},
		{"the recommendation", func(e *questions.Escalation) { e.Recommendation = long }, "recommendation"},
		{"the rationale", func(e *questions.Escalation) { e.RecommendationRationale = long }, "recommendation_rationale"},
		{"the option count", func(e *questions.Escalation) { e.Options = slices.Repeat([]string{"x"}, 21) }, "20 options"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			ops, store, id, holder := heldQuestion(t)
			escalation := questions.Escalation{
				Reason: questions.ReasonScopeChange, Options: []string{"Monogram", "Wordmark"}, Recommendation: "Wordmark",
				RecommendationRationale: "It reads at small sizes.",
			}
			tt.change(&escalation)
			_, err := ops.Escalate(ctx, "w", id, escalation, holder)
			if !errors.Is(err, questions.ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not name %q", err, tt.want)
			}
			var read questions.Question
			if err := store.Read(ctx, "w", func(tx ports.ReadTx) error {
				var err error
				read, err = tx.Questions().Question(id)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if read.Status == questions.StatusNeedsHuman {
				t.Errorf("a refused escalation left the question %s", read.Status)
			}
		})
	}
}
