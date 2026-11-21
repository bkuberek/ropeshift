package worker_test

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// consultCall is the call an orchestrator's worker sends to begin a consultation, with a target of its choosing.
func (r *toolRig) consultCall(t *testing.T, question, target string) ledger.OrchestratorCall {
	t.Helper()
	body, err := json.Marshal(ledger.ConsultRequest{Ask: consultAsk})
	if err != nil {
		t.Fatal(err)
	}
	return ledger.OrchestratorCall{
		Auth: r.subject.Auth(), Epoch: r.subject.Session().Epoch, Lane: r.row(t).Lane(), Question: question, Target: target, Body: body,
	}
}

// grants sends a consult call to the ledger and reads the grant it answers.
func (r *toolRig) grants(t *testing.T, question, target string) ledger.ConsultGrant {
	t.Helper()
	reply, err := r.plane.Orchestrate(t.Context(), ledger.VerbConsult, r.consultCall(t, question, target))
	if err != nil {
		t.Fatalf("consult %s as %s: %v", question, target, err)
	}
	var grant ledger.ConsultGrant
	if err := json.Unmarshal(reply.Result, &grant); err != nil {
		t.Fatal(err)
	}
	return grant
}

// FR-ORCH-9, FR-SPEND-6: the consultations that run count against what the project's daily and weekly limits leave, so
// two that run at once cannot spend more together than a limit that either alone fits under. A consultation's budget is
// held against the limits from its start until its end, which counts what it spent in their place.
func TestParallelConsultationsShareWhatTheProjectsLimitsLeave(t *testing.T) {
	t.Parallel()
	t.Run("each takes what the ones that run left", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		r.asked(t, "q2", "Which log level?")
		r.asked(t, "q3", "Which key format?")
		projectLimitOf(t, r.store, dailyLimit, 7.5)
		if first := r.grants(t, "q1", "c-first"); math.Abs(first.BudgetUSD-2) > 1e-9 {
			t.Errorf("the first consultation's budget is %v, and the consultation limit is 2", first.BudgetUSD)
		}
		if second := r.grants(t, "q2", "c-second"); math.Abs(second.BudgetUSD-0.5) > 1e-9 {
			t.Errorf("the second consultation's budget is %v, and the day leaves $2.50 less the $2 the first holds", second.BudgetUSD)
		}
		_, err := r.plane.Orchestrate(t.Context(), ledger.VerbConsult, r.consultCall(t, "q3", "c-third"))
		if !errors.Is(err, workorders.ErrIneligible) || !strings.Contains(err.Error(), "daily") {
			t.Errorf("a third consultation of a day the two that run hold: %v", err)
		}
		if row := r.question(t, "q3"); row.ClaimedBy != "" || r.startsOf(t, "q3") != 0 {
			t.Errorf("a refused consultation held or recorded something: %+v", row)
		}
		// The call that began a consultation, sent again, still answers with its grant, and holds nothing more.
		if again := r.grants(t, "q1", "c-first"); math.Abs(again.BudgetUSD-2) > 1e-9 {
			t.Errorf("the first call sent again answers a budget of %v", again.BudgetUSD)
		}
	})
	t.Run("a weekly limit is held the same way", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		r.asked(t, "q2", "Which log level?")
		publishSpend(t, r.store, "project_weekly_limit", "3")
		if first := r.grants(t, "q1", "c-first"); math.Abs(first.BudgetUSD-2) > 1e-9 {
			t.Errorf("the first consultation's budget is %v", first.BudgetUSD)
		}
		if second := r.grants(t, "q2", "c-second"); math.Abs(second.BudgetUSD-1) > 1e-9 {
			t.Errorf("the second consultation's budget is %v, and the week leaves $3 less the $2 the first holds", second.BudgetUSD)
		}
	})
	t.Run("a project with no limit holds nothing back", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		r.asked(t, "q2", "Which log level?")
		r.grants(t, "q1", "c-first")
		if second := r.grants(t, "q2", "c-second"); math.Abs(second.BudgetUSD-2) > 1e-9 {
			t.Errorf("with no daily or weekly limit the second budget is %v", second.BudgetUSD)
		}
	})
	t.Run("an ended consultation gives its budget back", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		r.asked(t, "q2", "Which log level?")
		projectLimitOf(t, r.store, dailyLimit, 7.5)
		r.nextSession([]fake.DriverStep{{Bound: true}, r.reportsConsultation(t, consultReport), completed(1)})
		r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
		r.awaitConsulted(t, "q1")
		r.nextSession([]fake.DriverStep{{Bound: true}, {Hold: true}})
		// What the day leaves is $1.50 now that the first consultation spent $1, and none of it is held for the first.
		if second := r.consult(t, map[string]any{"question": "q2", "ask": consultAsk}); math.Abs(second.BudgetUSD-1.5) > 1e-9 {
			t.Errorf("the second consultation's budget is %v", second.BudgetUSD)
		}
	})
}

// FR-ORCH-9, FR-SPEND-6: a consultation's budget stays held against the project's limits past its hold on the question,
// until its end arrives, because a worker that went away may leave its session spending, and the end it records when it
// starts again is what that session spent. A worker that never comes back holds it for no longer than the reservation,
// counted from the consultation's start.
func TestAConsultationWhoseWorkerWentAwayHoldsItsBudgetUntilItsEndArrives(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	r.asked(t, "q2", "Which log level?")
	r.asked(t, "q3", "Which key format?")
	publishSpend(t, r.store, "project_weekly_limit", "3")
	if first := r.grants(t, "q1", "c-first"); math.Abs(first.BudgetUSD-2) > 1e-9 {
		t.Fatalf("the first consultation's budget is %v", first.BudgetUSD)
	}
	r.shiftDatabase(t, (ledger.ConsultHold + time.Minute).Milliseconds())
	if second := r.grants(t, "q2", "c-second"); math.Abs(second.BudgetUSD-1) > 1e-9 {
		t.Errorf("past the first's hold, the second consultation's budget is %v, and the week leaves $3 less the $2 the first holds", second.BudgetUSD)
	}
	r.shiftDatabase(t, (ledger.ConsultReservation - ledger.ConsultHold).Milliseconds())
	if third := r.grants(t, "q3", "c-third"); math.Abs(third.BudgetUSD-2) > 1e-9 {
		t.Errorf("past the first's reservation, the third consultation's budget is %v, and the week leaves $3 less the $1 the second holds",
			third.BudgetUSD)
	}
}

// memberActs is what a member does to a question while its consultation may run: decide it now, or dismiss it.
type memberActs func(t *testing.T, r *toolRig, question string)

func decidesNow(t *testing.T, r *toolRig, question string) {
	t.Helper()
	if _, err := control.NewQuestions(r.store, questions.BatchPolicy{}).DecideNow(t.Context(), scope, question, member); err != nil {
		t.Fatal(err)
	}
}

func dismisses(t *testing.T, r *toolRig, question string) {
	t.Helper()
	if err := control.NewQuestions(r.store, questions.BatchPolicy{}).Dismiss(t.Context(), scope, question, member); err != nil {
		t.Fatal(err)
	}
}

// FR-ORCH-9, FR-SPEND-6: a consultation holds its budget against the project's limits from its start until its end,
// whatever becomes of its question meanwhile. A member who decides or dismisses the question
// while its session runs ends nothing the session spends, and a question that waits for a person is consulted on under
// the same hold, so neither lets a second consultation spend what the first holds.
func TestAConsultationHoldsItsBudgetWhateverBecomesOfItsQuestion(t *testing.T) {
	t.Parallel()
	for name, act := range map[string]memberActs{"decided now": decidesNow, "dismissed": dismisses} {
		t.Run("a question "+name+" while its consultation runs, then a second consultation", func(t *testing.T) {
			t.Parallel()
			r := aToolRig(t)
			r.asked(t, "q2", "Which log level?")
			projectLimitOf(t, r.store, dailyLimit, 7.5)
			r.grants(t, "q1", "c-first")
			act(t, r, "q1")
			if second := r.grants(t, "q2", "c-second"); math.Abs(second.BudgetUSD-0.5) > 1e-9 {
				t.Errorf("the second consultation's budget is %v, and the day leaves $2.50 less the $2 the first still holds", second.BudgetUSD)
			}
		})
		t.Run("a second consultation, then the first's question "+name, func(t *testing.T) {
			t.Parallel()
			r := aToolRig(t)
			r.asked(t, "q2", "Which log level?")
			r.asked(t, "q3", "Which key format?")
			projectLimitOf(t, r.store, dailyLimit, 7.5)
			r.grants(t, "q1", "c-first")
			if second := r.grants(t, "q2", "c-second"); math.Abs(second.BudgetUSD-0.5) > 1e-9 {
				t.Fatalf("the second consultation's budget is %v", second.BudgetUSD)
			}
			act(t, r, "q1")
			_, err := r.plane.Orchestrate(t.Context(), ledger.VerbConsult, r.consultCall(t, "q3", "c-third"))
			if !errors.Is(err, workorders.ErrIneligible) || !strings.Contains(err.Error(), "daily") {
				t.Errorf("a third consultation of a day the two that run hold: %v", err)
			}
		})
	}
	t.Run("a question that waits for a person", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		r.asked(t, "q2", "Which log level?")
		projectLimitOf(t, r.store, dailyLimit, 7.5)
		decidesNow(t, r, "q1")
		if row := r.question(t, "q1"); row.Status != questions.StatusNeedsHuman {
			t.Fatalf("the question is %s", row.Status)
		}
		if first := r.grants(t, "q1", "c-first"); math.Abs(first.BudgetUSD-2) > 1e-9 {
			t.Fatalf("the first consultation's budget is %v", first.BudgetUSD)
		}
		if second := r.grants(t, "q2", "c-second"); math.Abs(second.BudgetUSD-0.5) > 1e-9 {
			t.Errorf("the second consultation's budget is %v, and the day leaves $2.50 less the $2 the first holds", second.BudgetUSD)
		}
	})
}
