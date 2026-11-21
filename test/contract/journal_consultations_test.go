package contract

import (
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/roster"
)

func journaledConsultation(id, question string) ledger.ConsultationRun {
	return ledger.ConsultationRun{
		Grant: ledger.ConsultGrant{
			ID: id, Question: question, QuestionText: "Which retry budget?", Ask: "Compare the two.", BudgetUSD: 2, HoldUntil: 2_700_000,
			Agent:      roster.Agent{Name: "consultant", Class: "frontier", Approval: "per_use"},
			Repository: &ledger.ConsultRepository{ID: "retries", Name: "retries", Base: "main"},
		},
		Project: "pa", Orchestrator: "orchestrator-1", StartedAt: 1_000,
	}
}

// TestJournalKeepsAConsultationFromItsStartUntilItsEnd holds the start of each consultation a worker runs until the event
// that carries its end is stored, in one write, so a restart finds a start only for a consultation whose end the worker
// never journaled, and a consultation ends in the journal once.
func TestJournalKeepsAConsultationFromItsStartUntilItsEnd(t *testing.T) {
	t.Parallel()
	j := openTestJournal(t)
	ctx := t.Context()
	if got, err := j.Consultations(ctx); err != nil || len(got) != 0 {
		t.Fatalf("a new journal holds %+v, %v", got, err)
	}
	first, second := journaledConsultation("c-first", "q1"), journaledConsultation("c-second", "q2")
	for _, run := range []ledger.ConsultationRun{first, second, first} {
		if err := j.BeginConsultation(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	got, err := j.Consultations(ctx)
	if err != nil || !reflect.DeepEqual(got, []ledger.ConsultationRun{first, second}) {
		t.Fatalf("after two starts, one of them twice: %+v, %v", got, err)
	}
	end := ledger.UploadEvent{Epoch: 1, Type: ledger.UploadConsultationEnded, Project: "pa", Payload: []byte(`{"id":"c-first"}`), OccurredAt: 5}
	stored, journaled, err := j.EndConsultation(ctx, "c-first", end)
	if err != nil || !journaled || stored.Seq == 0 || stored.Type != ledger.UploadConsultationEnded {
		t.Fatalf("the end of a consultation the journal holds: %+v, %v, %v", stored, journaled, err)
	}
	if got, err = j.Consultations(ctx); err != nil || !reflect.DeepEqual(got, []ledger.ConsultationRun{second}) {
		t.Errorf("after the first ended: %+v, %v", got, err)
	}
	if types := pendingTypes(t, ctx, j); !reflect.DeepEqual(types, []string{ledger.UploadConsultationEnded}) {
		t.Errorf("the journal holds %v to upload", types)
	}
	if again, journaled, err := j.EndConsultation(ctx, "c-first", end); err != nil || journaled || again.Seq != 0 {
		t.Errorf("a consultation ended twice: %+v, %v, %v", again, journaled, err)
	}
	if _, journaled, err := j.EndConsultation(ctx, "c-ghost", end); err != nil || journaled {
		t.Errorf("a consultation the journal never held: %v, %v", journaled, err)
	}
	if types := pendingTypes(t, ctx, j); len(types) != 1 {
		t.Errorf("a repeat stored an event: %v", types)
	}
}

// TestJournalKeepsWhatAConsultationsSessionHasCost holds the highest cost a worker read of a consultation's session with
// its start, so a worker that starts again ends the consultation with what the session spent before, and a lower total
// read later, or a total of a consultation the journal does not hold, changes nothing.
func TestJournalKeepsWhatAConsultationsSessionHasCost(t *testing.T) {
	t.Parallel()
	j := openTestJournal(t)
	ctx := t.Context()
	run := journaledConsultation("c-first", "q1")
	if err := j.BeginConsultation(ctx, run); err != nil {
		t.Fatal(err)
	}
	for _, cost := range []float64{0.25, 0.7, 0.5} {
		if err := j.SaveConsultationCost(ctx, "c-first", cost); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.SaveConsultationCost(ctx, "c-ghost", 1); err != nil {
		t.Fatalf("the cost of a consultation the journal never held: %v", err)
	}
	run.CostUSD = 0.7
	got, err := j.Consultations(ctx)
	if err != nil || !reflect.DeepEqual(got, []ledger.ConsultationRun{run}) {
		t.Errorf("after three totals, the highest 0.7: %+v, %v", got, err)
	}
	if err := j.BeginConsultation(ctx, journaledConsultation("c-first", "q1")); err != nil {
		t.Fatal(err)
	}
	if got, err := j.Consultations(ctx); err != nil || len(got) != 1 || got[0].CostUSD != 0.7 {
		t.Errorf("a start sent again replaced what the session had cost: %+v, %v", got, err)
	}
}
