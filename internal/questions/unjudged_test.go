package questions

import (
	"reflect"
	"testing"
)

func lanesDraft(t *testing.T, categories ...string) Question {
	t.Helper()
	draft, err := New(Question{ID: "q", Text: "Which ID scheme do the work orders take?", Categories: categories, Blocking: true})
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

// A question no orchestrator took reaches a person from the core (FR-QD-3): the lookup rungs list what the core found by
// text and judged none of it, rungs 3 and 4 say no orchestrator worked them, and the question passes the gate's
// structure with no options and no recommendation, because nobody stated any.
func TestAQuestionNoOrchestratorTookReachesAPerson(t *testing.T) {
	got := Unjudged(lanesDraft(t, CategoryProduct), DefaultPolicy(), []string{"d-1"}, []string{"q-2"})
	want := []Step{
		{Rung: RungLookup, Candidates: []string{"d-1"}, Outcome: OutcomeUnjudged},
		{Rung: RungOpen, Candidates: []string{"q-2"}, Outcome: OutcomeUnjudged},
		{Rung: RungDecide, Outcome: OutcomeNoOrchestrator},
		{Rung: RungAssume, Outcome: OutcomeNoOrchestrator},
	}
	if !reflect.DeepEqual(got.LadderTrace, want) {
		t.Errorf("trace %+v, want %+v", got.LadderTrace, want)
	}
	if got.Status != StatusNeedsHuman || got.Rung != RungEscalate || got.EscalationReason != ReasonScopeChange {
		t.Errorf("question %+v", got)
	}
	if !got.NoOrchestrator() {
		t.Error("the question does not say no orchestrator judged it")
	}
}

// A one-way category keeps its reason: no orchestrator may decide it, so the item reads as one-way.
func TestAOneWayQuestionNoOrchestratorTookStaysOneWay(t *testing.T) {
	got := Unjudged(lanesDraft(t, CategorySecurity), DefaultPolicy(), nil, nil)
	if got.EscalationReason != ReasonOneWay {
		t.Errorf("reason %q, want %q", got.EscalationReason, ReasonOneWay)
	}
}

// An orchestrator that stopped after a rung keeps what it recorded, and the core adds only the rungs nobody ran. Its
// hold ends, because the question now waits for a person.
func TestTheCoreKeepsTheRungsAnOrchestratorRan(t *testing.T) {
	draft := lanesDraft(t, CategoryProduct)
	draft.LadderTrace = []Step{{Rung: RungLookup, Candidates: []string{"d-9"}, Outcome: "no answer"}}
	draft.Rung, draft.ClaimedBy, draft.ClaimUntil = RungOpen, "orchestrator:w", 5
	got := Unjudged(draft, DefaultPolicy(), []string{"d-1"}, nil)
	if len(got.LadderTrace) != 4 || got.LadderTrace[0].Outcome != "no answer" || got.LadderTrace[1].Rung != RungOpen {
		t.Errorf("trace %+v", got.LadderTrace)
	}
	if got.ClaimedBy != "" || got.ClaimUntil != 0 {
		t.Errorf("the hold stayed: %+v", got)
	}
}

// A question an orchestrator escalated says nothing of the kind.
func TestAnEscalatedQuestionSaysAnOrchestratorJudgedIt(t *testing.T) {
	draft := lanesDraft(t, CategoryProduct)
	draft.LadderTrace = []Step{{Rung: RungLookup}, {Rung: RungOpen}}
	if draft.NoOrchestrator() {
		t.Error("a question an orchestrator climbed reads as unjudged")
	}
}

// The item a member reads says no orchestrator judged the question, and holds its ladder trace so far.
func TestTheItemSaysNoOrchestratorJudgedIt(t *testing.T) {
	row := Unjudged(lanesDraft(t, CategoryProduct), DefaultPolicy(), []string{"d-1"}, nil)
	item := Present(Batch{ID: "b"}, []Question{row}, "", DefaultPolicy()).Items[0]
	if !item.NoOrchestrator || len(item.LadderTrace) != 4 || !reflect.DeepEqual(item.Citations, []string{"d-1"}) {
		t.Errorf("item %+v", item)
	}
}
