package exit

import (
	"strconv"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

const hour = time.Hour

// e10Decision records a decision as the world's orchestrator does.
func e10Decision(t *testing.T, w *apitest.World, id, statement string) questions.Decision {
	t.Helper()
	decision, err := w.Questions.RecordDecision(t.Context(), apitest.Scope, questions.Decision{
		ID: id, Status: questions.DecisionDecided, Statement: statement, Rationale: "It follows.", Citations: []string{"PAY-12"},
		Scope: "retries-" + id, Reversibility: questions.Reversible, Categories: []string{questions.CategoryProduct},
	}, ledger.Actor{Kind: ledger.ActorOrchestrator, ID: apitest.WorldWorker})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

// databaseNow reads the time the ledger stamps writes with.
func databaseNow(t *testing.T, d *apitest.Daemon) int64 {
	t.Helper()
	var now int64
	err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		var err error
		now, err = tx.Now()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return now
}

func wantRatio(t *testing.T, name string, got measures.Ratio, numerator, denominator int64) {
	t.Helper()
	if got.Numerator != numerator || got.Denominator != denominator || got.Value == nil ||
		*got.Value != float64(numerator)/float64(denominator) {
		t.Errorf("%s is %+v, want %d over %d", name, got, numerator, denominator)
	}
}

func wantDurations(t *testing.T, name string, got measures.Durations, count int64, median, p95 time.Duration) {
	t.Helper()
	if got.Count != count || got.MedianMs == nil || got.P95Ms == nil || *got.MedianMs != median.Milliseconds() || *got.P95Ms != p95.Milliseconds() {
		t.Errorf("%s is %+v, want %d with median %s and 95th percentile %s", name, got, count, median, p95)
	}
}

// TestExitE10MeasuresFromLedger scripts a history through the API and the use cases, with a known
// answer for every product measure, on a daemon that has no telemetry pipeline at all: the measures
// come from the ledger alone (FR-OBS-6). Three questions reach a person in one batch, and the batch
// counts once. The north star stands beside the overturn rate and rework in the operations view, as
// it does in the measures' own read (FR-OBS-7).
func TestExitE10MeasuresFromLedger(t *testing.T) {
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	w := apitest.NewWorld(t, d)
	ctx := t.Context()
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: apitest.WorldWorker}
	// A member's session lasts less than this history, so the member signs in again after each move.
	advance := func(by time.Duration) {
		clock.Advance(by)
		if _, err := apitest.SignIn(ctx, d.Client); err != nil {
			t.Fatal(err)
		}
	}

	// An orchestrator decides once, before anything is asked.
	earlier := e10Decision(t, w, "decision-earlier", "Retry with the same key")
	advance(hour)

	// Three questions climb to a person and reach them in one batch: one interruption.
	asked := []string{
		w.Escalated("May we drop the retry budget?", []string{questions.CategoryProduct}, false),
		w.Escalated("Should a retry reuse its key?", []string{questions.CategoryProduct}, false),
		w.Escalated("Who owns the payment provider?", []string{questions.CategoryProduct}, false),
	}
	advance(hour)
	w.Deliver()
	batches, err := d.Client.Batches(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || len(batches[0].Items) != 3 {
		t.Fatalf("batches %+v", batches)
	}
	// Another lane asks the third again, and waits on the one already asked: a repeat prevented.
	again, err := w.Questions.Ask(ctx, apitest.Scope, questions.Ask{
		ID: "question-again", Lane: "lane-other", Generation: 1, Text: "Who owns the payment provider?",
		Categories: []string{questions.CategoryProduct},
	}, ledger.Actor{Kind: ledger.ActorLane, ID: "lane-other"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != questions.AskAttached || again.QuestionID != asked[2] {
		t.Fatalf("the repeat was not attached: %+v", again)
	}
	// A person answers the first, and answers the second by citing what was decided before it was
	// asked: a repeated question.
	if _, err := d.Client.AnswerQuestion(ctx, asked[0], api.AnswerQuestion{ID: "decision-answer", Answer: "Keep it"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.AnswerQuestion(ctx, asked[1], api.AnswerQuestion{
		ID: "decision-repeat", Answer: "As decided", Citations: []string{earlier.ID},
	}); err != nil {
		t.Fatal(err)
	}
	// A fourth question closes at rung 1, with no person.
	fourth, err := w.Questions.Ask(ctx, apitest.Scope, questions.Ask{
		ID: "question-4", Lane: "lane-asking", Generation: 1, Text: "Does a retry keep its idempotency key?",
		Categories: []string{questions.CategoryProduct},
	}, ledger.Actor{Kind: ledger.ActorLane, ID: "lane-asking"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Questions.Take(ctx, apitest.Scope, fourth.QuestionID, apitest.LeaseTTL, orchestrator); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Questions.Climb(ctx, apitest.Scope, fourth.QuestionID, questions.Step{Rung: questions.RungLookup, Candidates: []string{earlier.ID}, Outcome: "answers"}, orchestrator); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Questions.Resolve(ctx, apitest.Scope, fourth.QuestionID, earlier.ID, orchestrator); err != nil {
		t.Fatal(err)
	}
	// The orchestrator decides once more, and a person overturns it: one of two overturned.
	later := e10Decision(t, w, "decision-later", "Retry three times")
	if _, err := d.Client.OverturnDecision(ctx, later.ID, api.OverturnDecision{ID: "decision-overturn", Statement: "Retry twice"}); err != nil {
		t.Fatal(err)
	}

	// Two work orders run and a person accepts them, two and four hours after each became ready. One
	// lane reported a turn with no cost, so the total is a lower bound.
	for i, run := range []struct {
		id      string
		takes   time.Duration
		cost    float64
		unknown int64
	}{{"wo-a", 2 * hour, 0.5, 0}, {"wo-b", 4 * hour, 1.5, 1}} {
		lane := w.Running(run.id, "Ship part "+strconv.Itoa(i))
		advance(run.takes)
		lane.Finish(workorders.LaneResult{
			Report: workorders.Report{Outcome: workorders.OutcomeCompleted}, Branch: "lane/" + lane.ID, Head: "abc123",
			CostUSD: run.cost, UnknownCostTurns: run.unknown,
		})
		if _, err := d.Client.Accept(ctx, run.id); err != nil {
			t.Fatal(err)
		}
	}

	// A person decides an approval: one interruption.
	asking := w.Running("wo-c", "Run the tests")
	approval := asking.Approval("approval-1", `{"command":"go test ./..."}`)
	if _, err := d.Client.DecideApproval(ctx, approval.ID, api.DecideApproval{Digest: approval.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	// A person says what became of an effect a crash left unknown: one forced action.
	crashed := w.Running("wo-d", "Apply the migration")
	effect := crashed.Effect("lane-wo-d/1/input/brief", "input", 1)
	if _, err := d.Client.DecideEffect(ctx, effect.ID, api.DecideEffect{Outcome: ledger.EffectHappened}); err != nil {
		t.Fatal(err)
	}
	// A lane parks on a timer, and its wake fires half an hour later.
	parked := w.Running("wo-e", "Wait for the window")
	parked.Park(`{"at": ` + strconv.FormatInt(databaseNow(t, d)+(30*time.Minute).Milliseconds(), 10) + `}`)
	advance(30 * time.Minute)
	if _, err := control.NewWakes(d.Store).WakeTick(ctx, apitest.Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "wake-tick"}); err != nil {
		t.Fatal(err)
	}

	report, err := d.Client.Measures(ctx, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if report.Interruptions != 3 || report.InterruptionsByKind[measures.InterruptionBatch] != 1 ||
		report.InterruptionsByKind[measures.InterruptionApproval] != 1 || report.InterruptionsByKind[measures.InterruptionForced] != 1 {
		t.Errorf("interruptions %d %v, want one batch, one approval, and one forced action", report.Interruptions, report.InterruptionsByKind)
	}
	if report.Completed != 2 {
		t.Errorf("completed %d", report.Completed)
	}
	wantRatio(t, "interruptions per completed work order", report.InterruptionsPerCompleted, 3, 2)
	wantRatio(t, "overturn rate", report.OverturnRate, 1, 2)
	wantRatio(t, "rework", report.Rework, 0, 2)
	wantRatio(t, "autonomous decision rate", report.AutonomousDecisionRate, 1, 3)
	wantRatio(t, "repeated-question rate", report.RepeatedQuestionRate, 1, 3)
	if report.PreventedRepeats != 1 {
		t.Errorf("prevented repeats %d", report.PreventedRepeats)
	}
	wantDurations(t, "ready to done", report.ReadyToDone, 2, 2*hour, 4*hour)
	wantDurations(t, "intake to done", report.IntakeToDone, 2, 2*hour, 4*hour)
	wantDurations(t, "park time on a timer", report.ParkTime["tick"], 1, 30*time.Minute, 30*time.Minute)
	if report.WorkInProgress != 2 {
		t.Errorf("work in progress %d, want the approval's lane and the effect's", report.WorkInProgress)
	}
	change := report.CostPerOutcome[string(workorders.KindChange)]
	if change.TotalUSD != 2 || change.Done != 2 || change.PerDone == nil || *change.PerDone != 1 || !change.LowerBound {
		t.Errorf("cost per outcome %+v", change)
	}

	var operations api.Operations
	if err := d.Client.ReadView(ctx, api.ViewOperations, "", &operations); err != nil {
		t.Fatal(err)
	}
	if got := operations.Measures.InterruptionsPerCompleted; got.Value == nil || *got.Value != 1.5 ||
		operations.Measures.OverturnRate.Denominator != 2 || operations.Measures.Rework.Denominator != 2 {
		t.Errorf("the operations view shows %+v", operations.Measures)
	}
	if telemetry, err := d.Client.LaneTelemetry(ctx); err != nil || telemetry.Available {
		t.Errorf("this daemon was meant to have no telemetry pipeline: %+v %v", telemetry, err)
	}
}
