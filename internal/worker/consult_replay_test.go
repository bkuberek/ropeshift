package worker_test

import (
	"encoding/json"
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// startsOf counts the consultation starts the ledger recorded for a question.
func (r *toolRig) startsOf(t *testing.T, question string) int {
	t.Helper()
	var starts int
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectConsultation, ID: question})
		for _, event := range events {
			if event.Type == ledger.EventConsultationStarted {
				starts++
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return starts
}

// FR-ORCH-9: a consult call sent again with the same target by the worker that sent it is the same call, so it answers
// with the grant the ledger recorded and holds and records nothing more, while another target is a second consultation
// of the question and is refused.
func TestAConsultCallSentAgainIsReplayedNotRefused(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	body, err := json.Marshal(ledger.ConsultRequest{Ask: consultAsk})
	if err != nil {
		t.Fatal(err)
	}
	call := ledger.OrchestratorCall{
		Auth: r.subject.Auth(), Epoch: r.subject.Session().Epoch, Lane: r.row(t).Lane(), Question: "q1", Target: "c-sent-twice", Body: body,
	}
	first, err := r.plane.Orchestrate(t.Context(), ledger.VerbConsult, call)
	if err != nil {
		t.Fatal(err)
	}
	held := r.question(t, "q1")
	second, err := r.plane.Orchestrate(t.Context(), ledger.VerbConsult, call)
	if err != nil {
		t.Fatalf("the same call sent again was refused: %v", err)
	}
	if string(second.Result) != string(first.Result) {
		t.Errorf("the replay answers %s, and the first call answered %s", second.Result, first.Result)
	}
	if again := r.question(t, "q1"); again.ClaimedBy != held.ClaimedBy || again.ClaimUntil != held.ClaimUntil {
		t.Errorf("the replay changed the hold from %+v to %+v", held, again)
	}
	if starts := r.startsOf(t, "q1"); starts != 1 {
		t.Errorf("the ledger recorded %d starts", starts)
	}
	other := call
	other.Target = "c-another"
	if _, err := r.plane.Orchestrate(t.Context(), ledger.VerbConsult, other); !errors.Is(err, questions.ErrTaken) {
		t.Errorf("a second consultation of the question: %v", err)
	}
}
