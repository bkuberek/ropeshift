package server_test

import (
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

// aQuestionOnTheLadder is a lane's question that no orchestrator has judged yet.
func aQuestionOnTheLadder(t *testing.T, w *apitest.World, id string) {
	t.Helper()
	ask := questions.Ask{ID: id, Text: "How many retries? " + id, Categories: []string{questions.CategoryProduct}, Lane: "lane-" + id, Generation: 1}
	if _, err := w.Questions.Ask(t.Context(), apitest.Scope, ask, ledger.Actor{Kind: ledger.ActorLane, ID: ask.Lane}); err != nil {
		t.Fatal(err)
	}
}

// Decide now takes a question off the ladder into the member's Needs you at once: it waits for a person in a batch
// that reached them, its ladder says a member chose to decide it now, and the audit names the operation.
func TestDecideNowTakesAQuestionOffTheLadderIntoNeedsYou(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	aQuestionOnTheLadder(t, w, "question-1")
	got, err := d.Client.DecideNow(t.Context(), "question-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != questions.StatusNeedsHuman || got.BatchID == "" {
		t.Fatalf("decide now left %+v", got)
	}
	last := got.LadderTrace[len(got.LadderTrace)-1]
	if last.Outcome != questions.OutcomeDecidedNow || last.By == "" {
		t.Errorf("the ladder ends with %+v", last)
	}
	found := false
	for _, need := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if need.Kind == api.NeedQuestions && need.ID == got.BatchID {
			for _, item := range need.Batch.Items {
				found = found || item.ID == "question-1"
			}
		}
	}
	if !found {
		t.Error("Needs you holds no batch with the question")
	}
	again, err := d.Client.DecideNow(t.Context(), "question-1")
	if err != nil || again.BatchID != got.BatchID {
		t.Errorf("deciding it now again read %+v: %v", again, err)
	}
}

// A delegate that calls question.decide_now is refused as member-only, and the question stays on the ladder.
func TestADelegatesDecideNowIsRefused(t *testing.T) {
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	aQuestionOnTheLadder(t, w, "question-1")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	_, err := delegate.DecideNow(t.Context(), "question-1")
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	row, err := d.Client.Question(t.Context(), "question-1")
	if err != nil || row.Status != questions.StatusOpen {
		t.Errorf("a refused decide now left the question %+v: %v", row, err)
	}
}

// A question that no longer waits has nothing to decide now.
func TestDecideNowRefusesAQuestionThatNoLongerWaits(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	aQuestionOnTheLadder(t, w, "question-1")
	if err := d.Client.DismissQuestion(t.Context(), "question-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.DecideNow(t.Context(), "question-1"); err == nil {
		t.Fatal("a dismissed question was decided now")
	}
}
