package questions

import (
	"errors"
	"testing"
)

func plannersDraft(t *testing.T) Question {
	t.Helper()
	draft, err := New(Question{ID: "q", Text: "Who signs in to the admin console?", Categories: []string{CategoryProduct}})
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

// A planning lane's question goes to the person (FR-SESSION-3): it stands at the last rung with no
// ladder trace, and it needs no options and no recommendation.
func TestAQuestionForThePersonSkipsTheLadder(t *testing.T) {
	got, err := ToPerson(plannersDraft(t))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusNeedsHuman || got.Rung != RungEscalate || len(got.LadderTrace) != 0 || got.EscalationReason != ReasonPlannerAsked {
		t.Errorf("question %+v", got)
	}
	if !got.Direct() {
		t.Error("a question for the person does not read as direct")
	}
	if len(got.Options) != 0 || got.Recommendation != "" {
		t.Errorf("the question gained options or a recommendation: %+v", got)
	}
}

// With no gate to name a category, the question needs one when it is asked, because members find
// questions by category.
func TestAQuestionForThePersonNeedsACategory(t *testing.T) {
	draft := plannersDraft(t)
	draft.Categories = nil
	if _, err := ToPerson(draft); !errors.Is(err, ErrInvalid) {
		t.Errorf("a question with no category: %v", err)
	}
}

// Only a planner's question is direct. A question that climbed the ladder is not, and the gate does
// not accept the planner's reason, so an orchestrator cannot make a question direct.
func TestOnlyAPlannersQuestionIsDirect(t *testing.T) {
	if escalatable().Direct() {
		t.Error("an escalated question reads as direct")
	}
	forged := escalatable()
	forged.EscalationReason = ReasonPlannerAsked
	failed := Gate(forged)
	if len(failed) != 1 || failed[0].Check != GateReason {
		t.Errorf("the gate let the planner's reason through: %v", failed)
	}
}
