package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// tickActor is who a batch's delivery names.
var tickActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "batch-tick"}

// BatchChecks hold what reaches a person: a window on database time, a batch that counts
// once, and an answer given once.
var BatchChecks = []Check{
	{"B1", "questions wait for the window, then reach members as one batch", checkBatchWindow},
	{"B2", "a blocking question closes its batch early", checkBatchBlocking},
	{"B3", "a question that starts blocking closes its open batch early", checkBatchBecomesBlocking},
	{"B4", "a batch of five records one interruption", checkBatchCountsOnce},
	{"B5", "racing deliveries deliver once", checkBatchRace},
	{"B6", "a batch nobody needs reaches nobody", checkBatchEmpty},
	{"B7", "a member sees options, a recommendation, and citations, by category", checkBatchPresented},
	{"B8", "a member answers once, and a retry finds that answer", checkAnswerOnce},
	{"B9", "a member answers an open question with no ladder and no orchestrator", checkAnswerDirectly},
	{"B10", "only a member answers", checkAnswerActors},
	{"B11", "racing answers have one winner", checkAnswerRace},
	{"B12", "an answer ends an orchestrator's hold", checkAnswerEndsHold},
	{"B13", "an answer lets held work return", checkAnswerReleasesWork},
}

// escalated takes a question up the ladder to a person.
func (r *Rig) escalated(s ports.Store, text string, change func(*questions.Ask)) (string, error) {
	ops := r.NewOps(s)
	holder := r.orchestrator("holder")
	asked, err := r.ask(s, text, "lane-a", change)
	if err != nil {
		return "", err
	}
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, holder); err != nil {
		return "", err
	}
	if err := r.climbed(s, asked.QuestionID, holder); err != nil {
		return "", err
	}
	_, err = ops.Escalate(r.Ctx, r.Scope, asked.QuestionID, questions.Escalation{
		Reason: questions.ReasonScopeChange, Options: []string{"Keep", "Change"}, Recommendation: "Keep",
	}, holder)
	return asked.QuestionID, err
}

func blocking(a *questions.Ask) { a.Blocking = true }

// deliveries reads the interruptions the log holds.
func (r *Rig) deliveries(s ports.Store) ([]questions.Delivery, error) {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return nil, err
	}
	var out []questions.Delivery
	for _, event := range events {
		if event.Type != questions.EventBatchDelivered {
			continue
		}
		var delivery questions.Delivery
		if err := json.Unmarshal(event.Data, &delivery); err != nil {
			return nil, err
		}
		out = append(out, delivery)
	}
	return out, nil
}

// noDeliveryYet ticks once and proves nothing reached anyone.
func (r *Rig) noDeliveryYet(s ports.Store) error {
	if _, err := r.NewOps(s).DeliverBatches(r.Ctx, r.Scope, tickActor); err != nil {
		return err
	}
	delivered, err := r.deliveries(s)
	if err != nil {
		return err
	}
	if len(delivered) != 0 {
		return Violatedf("delivered early: %+v", delivered)
	}
	return nil
}

func (r *Rig) deliveredOnce(s ports.Store, want ...string) error {
	if _, err := r.NewOps(s).DeliverBatches(r.Ctx, r.Scope, tickActor); err != nil {
		return err
	}
	delivered, err := r.deliveries(s)
	if err != nil {
		return err
	}
	if len(delivered) != 1 {
		return Violatedf("%d deliveries, want 1", len(delivered))
	}
	got := slices.Sorted(slices.Values(delivered[0].Questions))
	if !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		return Violatedf("delivered %v, want %v", got, want)
	}
	return nil
}

func checkBatchWindow(r *Rig) error {
	s := r.Store()
	first, err := r.escalated(s, "What is the retry budget?", nil)
	if err != nil {
		return err
	}
	if err := r.noDeliveryYet(s); err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs / 2)
	second, err := r.escalated(s, "Which currency do refunds use?", nil)
	if err != nil {
		return err
	}
	if err := r.noDeliveryYet(s); err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs/2 + 1)
	return r.deliveredOnce(s, first, second)
}

func checkBatchBlocking(r *Rig) error {
	s := r.Store()
	id, err := r.escalated(s, "May audit data leave the region?", blocking)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(BatchMaxWaitMs / 2)
	if err := r.noDeliveryYet(s); err != nil {
		return err
	}
	r.DB.ShiftClock(BatchMaxWaitMs/2 + 1)
	return r.deliveredOnce(s, id)
}

func checkBatchBecomesBlocking(r *Rig) error {
	s := r.Store()
	id, err := r.escalated(s, "May audit data leave the region?", nil)
	if err != nil {
		return err
	}
	if _, err := r.ask(s, "May audit data leave the region?", "lane-b", blocking); err != nil {
		return err
	}
	r.DB.ShiftClock(BatchMaxWaitMs + 1)
	return r.deliveredOnce(s, id)
}

func checkBatchCountsOnce(r *Rig) error {
	s := r.Store()
	var ids []string
	for i := range 5 {
		id, err := r.escalated(s, fmt.Sprintf("Question number %d?", i), nil)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	if err := r.deliveredOnce(s, ids...); err != nil {
		return err
	}
	if err := r.deliveredOnce(s, ids...); err != nil {
		return err
	}
	if _, err := r.escalated(s, "A question after the batch?", nil); err != nil {
		return err
	}
	return r.deliveredOnce(s, ids...)
}

func checkBatchRace(r *Rig) error {
	s := r.Store()
	id, err := r.escalated(s, "What is the retry budget?", nil)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	ops := r.NewOps(s)
	const ticks = 8
	var wg sync.WaitGroup
	failures := make([]error, ticks)
	for i := range ticks {
		wg.Go(func() {
			_, failures[i] = ops.DeliverBatches(r.Ctx, r.Scope, tickActor)
		})
	}
	wg.Wait()
	if err := errors.Join(failures...); err != nil {
		return err
	}
	delivered, err := r.deliveries(s)
	if err != nil {
		return err
	}
	if len(delivered) != 1 || !slices.Equal(delivered[0].Questions, []string{id}) {
		return Violatedf("%d deliveries: %+v", len(delivered), delivered)
	}
	return r.closesOnce(s, delivered[0].Batch.ID)
}

// closesOnce calls the compare-and-set a second time on a closed batch, which is the race
// a tick can lose, and which racing goroutines do not reach every run.
func (r *Rig) closesOnce(s ports.Store, id string) error {
	moved := false
	err := s.Write(r.Ctx, r.Scope, "note", func(tx ports.WriteTx) error {
		var err error
		if moved, err = tx.Questions().CloseBatch(id, questions.BatchDelivered, tx.Now()); err != nil {
			return err
		}
		_, err = tx.Append(r.noteDraft(id))
		return err
	})
	if err != nil {
		return err
	}
	if moved {
		return Violatedf("batch %s closed twice", id)
	}
	return nil
}

func checkBatchEmpty(r *Rig) error {
	s := r.Store()
	id, err := r.escalated(s, "What is the retry budget?", nil)
	if err != nil {
		return err
	}
	if _, err := r.answer(s, id, "Two retries."); err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	if err := r.noDeliveryYet(s); err != nil {
		return err
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		batches, err := tx.Questions().Batches()
		if err != nil {
			return err
		}
		if len(batches) != 1 || batches[0].Status != questions.BatchEmpty || batches[0].DeliveredAt != 0 {
			return Violatedf("batches %+v", batches)
		}
		return nil
	})
}

func (r *Rig) answer(s ports.Store, id, statement string) (questions.Decision, error) {
	return r.NewOps(s).Answer(r.Ctx, r.Scope, id, questions.Answer{
		DecisionID: r.ID("d"), Statement: statement, Rationale: "It follows.",
	}, r.Actor)
}

func checkBatchPresented(r *Rig) error {
	s := r.Store()
	product, err := r.escalated(s, "What is the retry budget?", nil)
	if err != nil {
		return err
	}
	data, err := r.escalated(s, "May audit data leave the region?", func(a *questions.Ask) { a.Categories = []string{questions.CategoryData} })
	if err != nil {
		return err
	}
	answered, err := r.escalated(s, "Which currency do refunds use?", nil)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	if err := r.deliveredOnce(s, product, data, answered); err != nil {
		return err
	}
	if _, err := r.answer(s, answered, "The original currency."); err != nil {
		return err
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		batches, err := tx.Questions().WaitingBatches()
		if err != nil {
			return err
		}
		if len(batches) != 1 {
			return Violatedf("%d waiting batches", len(batches))
		}
		rows, err := tx.Questions().BatchQuestions(batches[0].ID)
		if err != nil {
			return err
		}
		if all := questions.Present(batches[0], rows, ""); len(all.Items) != 2 {
			return Violatedf("an answered question is still presented: %+v", all.Items)
		}
		only := questions.Present(batches[0], rows, questions.CategoryData)
		if len(only.Items) != 1 || only.Items[0].ID != data {
			return Violatedf("the data category shows %+v", only.Items)
		}
		item := only.Items[0]
		if len(item.Options) != 2 || item.Recommendation != "Keep" || item.EscalationReason != questions.ReasonScopeChange {
			return Violatedf("an item a member cannot answer once: %+v", item)
		}
		return nil
	})
}

func checkAnswerOnce(r *Rig) error {
	s := r.Store()
	id, err := r.escalated(s, "What is the retry budget?", nil)
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	first := questions.Answer{DecisionID: r.ID("d"), Statement: "Two retries."}
	decision, err := ops.Answer(r.Ctx, r.Scope, id, first, r.Actor)
	if err != nil {
		return err
	}
	if decision.DecidedBy != r.Actor.ID || decision.QuestionID != id || decision.Status != questions.DecisionDecided {
		return Violatedf("decision %+v", decision)
	}
	row, err := r.readQuestion(s, id)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusAnswered || row.DecisionID != decision.ID {
		return Violatedf("question %+v", row)
	}
	retry, err := ops.Answer(r.Ctx, r.Scope, id, first, r.Actor)
	if err != nil || retry.ID != decision.ID {
		return Violatedf("a retry: %+v, %v", retry, err)
	}
	second := questions.Answer{DecisionID: r.ID("d"), Statement: "Three retries."}
	if _, err := ops.Answer(r.Ctx, r.Scope, id, second, r.Actor); !errors.Is(err, questions.ErrResolved) {
		return Violatedf("a second answer: %v", err)
	}
	return r.CheckQuestions(s)
}

// checkAnswerDirectly holds D25: with no orchestrator, nothing climbs the ladder, so a
// member can answer an open question with no trace.
func checkAnswerDirectly(r *Rig) error {
	s := r.Store()
	asked, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	decision, err := r.answer(s, asked.QuestionID, "Two retries.")
	if err != nil {
		return err
	}
	again, err := r.ask(s, "What is the retry budget?", "lane-b", nil)
	if err != nil {
		return err
	}
	if again.Status != questions.AskAnswered || again.DecisionID != decision.ID {
		return Violatedf("the next ask reads %+v", again)
	}
	return nil
}

func checkAnswerActors(r *Rig) error {
	s := r.Store()
	id, err := r.escalated(s, "What is the retry budget?", nil)
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	answer := questions.Answer{DecisionID: r.ID("d"), Statement: "Two retries."}
	for _, actor := range []string{"lane", "orchestrator"} {
		caller := r.laneActor("lane-a")
		if actor == "orchestrator" {
			caller = r.orchestrator("holder")
		}
		if _, err := ops.Answer(r.Ctx, r.Scope, id, answer, caller); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a %s answered: %v", actor, err)
		}
	}
	return nil
}

func checkAnswerRace(r *Rig) error {
	s := r.Store()
	id, err := r.escalated(s, "What is the retry budget?", nil)
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	const members = 8
	var wg sync.WaitGroup
	failures := make([]error, members)
	for i := range members {
		answer := questions.Answer{DecisionID: fmt.Sprintf("%s-%d", id, i), Statement: fmt.Sprintf("Answer %d.", i)}
		wg.Go(func() {
			_, failures[i] = ops.Answer(r.Ctx, r.Scope, id, answer, r.Actor)
		})
	}
	wg.Wait()
	won := 0
	for _, err := range failures {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, questions.ErrResolved):
			return err
		}
	}
	if won != 1 {
		return Violatedf("%d answers won", won)
	}
	return r.CheckQuestions(s)
}

func checkAnswerEndsHold(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	holder := r.orchestrator("holder")
	asked, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, holder); err != nil {
		return err
	}
	if _, err := r.answer(s, asked.QuestionID, "Two retries."); err != nil {
		return err
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if row.ClaimedBy != "" || row.ClaimUntil != 0 {
		return Violatedf("the hold stayed: %+v", row)
	}
	step := questions.Step{Rung: questions.RungLookup, Outcome: "no match"}
	if _, err := ops.Climb(r.Ctx, r.Scope, asked.QuestionID, step, holder); !errors.Is(err, questions.ErrResolved) {
		return Violatedf("the old holder climbed: %v", err)
	}
	return nil
}

func checkAnswerReleasesWork(r *Rig) error {
	s := r.Store()
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	asked, err := r.ask(s, "May this work start?", "lane-a", func(a *questions.Ask) {
		a.Blocking, a.SubjectKind, a.SubjectID = true, workorders.SubjectKind, id
	})
	if err != nil {
		return err
	}
	blocked, err := r.openBlocking(s, id)
	if err != nil {
		return err
	}
	if blocked != 1 {
		return Violatedf("%d blocking questions", blocked)
	}
	if _, err := r.answer(s, asked.QuestionID, "Yes."); err != nil {
		return err
	}
	blocked, err = r.openBlocking(s, id)
	if err != nil {
		return err
	}
	if blocked != 0 {
		return Violatedf("%d blocking questions after the answer", blocked)
	}
	return nil
}

func (r *Rig) openBlocking(s ports.Store, workOrder string) (int64, error) {
	var count int64
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		count, err = tx.Questions().OpenBlocking(workOrder, "", "")
		return err
	})
	return count, err
}
