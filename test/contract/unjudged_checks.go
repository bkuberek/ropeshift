package contract

import (
	"slices"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// UnjudgedChecks hold FR-QD-3: a question no live orchestrator took within its batch window reaches a person from the
// core.
var UnjudgedChecks = []Check{
	{"UJ1", "a question an orchestrator takes within its window stays the orchestrator's", checkUnjudgedTakenInWindow},
	{"UJ2", "a question no orchestrator takes reaches a person after its window, saying no orchestrator judged it", checkUnjudgedReachesAPerson},
	{"UJ3", "a question whose orchestrator stops mid-window reaches a person once its hold ends", checkUnjudgedOrchestratorStops},
}

const unjudgedText = "Which ID scheme do the work orders take?"

// deliveredHolds ticks once and reports whether a delivered batch holds a question.
func (r *Rig) deliveredHolds(s ports.Store, id string) (bool, error) {
	if _, err := r.NewOps(s).DeliverBatches(r.Ctx, r.Scope, tickActor); err != nil {
		return false, err
	}
	delivered, err := r.deliveries(s)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(delivered, func(d questions.Delivery) bool { return slices.Contains(d.Questions, id) }), nil
}

func checkUnjudgedTakenInWindow(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	asked, err := r.ask(s, unjudgedText, "lane-a", blocking)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs - TTLMs/2)
	holder := r.orchestrator("holder")
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, holder); err != nil {
		return err
	}
	r.DB.ShiftClock(TTLMs/2 + 1)
	reached, err := r.deliveredHolds(s, asked.QuestionID)
	if err != nil {
		return err
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if reached || row.ClaimedBy != holder.ID || row.Status != questions.StatusTriage || row.NoOrchestrator() {
		return Violatedf("a question an orchestrator holds past its window reads %+v, delivered %t", row, reached)
	}
	if err := r.climbed(s, asked.QuestionID, holder); err != nil {
		return Violatedf("the orchestrator cannot climb the question it holds: %v", err)
	}
	return nil
}

func checkUnjudgedReachesAPerson(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	cited, err := ops.RecordDecision(r.Ctx, r.Scope, questions.Decision{
		ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Work orders take sequential integer IDs.",
		Categories: []string{questions.CategoryProduct},
	}, r.Actor)
	if err != nil {
		return err
	}
	asked, err := r.ask(s, unjudgedText, "lane-a", nil)
	if err != nil {
		return err
	}
	if err := r.noDeliveryYet(s); err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	reached, err := r.deliveredHolds(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if !reached {
		return Violatedf("a question no orchestrator took waited past its window, and no batch holds it")
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusNeedsHuman || row.EscalationReason != questions.ReasonScopeChange || !row.NoOrchestrator() {
		return Violatedf("the question reads %+v", row)
	}
	var item questions.Item
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		batch, err := tx.Questions().Batch(row.BatchID)
		if err != nil {
			return err
		}
		policy, err := control.DecisionPolicy(tx)
		if err != nil {
			return err
		}
		presented := questions.Present(batch, []questions.Question{row}, "", policy)
		if len(presented.Items) != 1 {
			return Violatedf("the batch presents %d items", len(presented.Items))
		}
		item = presented.Items[0]
		return nil
	})
	if err != nil {
		return err
	}
	if !item.NoOrchestrator || !slices.Contains(item.Citations, cited.ID) || len(item.LadderTrace) != 4 {
		return Violatedf("the item reads %+v, want it unjudged with %s among its candidates", item, cited.ID)
	}
	return nil
}

func checkUnjudgedOrchestratorStops(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	holder := r.orchestrator("holder")
	stopped, err := r.ask(s, unjudgedText, "lane-a", blocking)
	if err != nil {
		return err
	}
	if err := r.climbed(s, stopped.QuestionID, holder); err != nil {
		return err
	}
	if _, err := ops.LetGo(r.Ctx, r.Scope, holder); err != nil {
		return err
	}
	crashed, err := r.ask(s, "Which logo set should the site use?", "lane-b", blocking)
	if err != nil {
		return err
	}
	if _, err := ops.Take(r.Ctx, r.Scope, crashed.QuestionID, TTLMs, r.orchestrator("crashed")); err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	for _, id := range []string{stopped.QuestionID, crashed.QuestionID} {
		reached, err := r.deliveredHolds(s, id)
		if err != nil {
			return err
		}
		if !reached {
			return Violatedf("question %s, whose orchestrator stopped, reached no batch", id)
		}
	}
	row, err := r.readQuestion(s, stopped.QuestionID)
	if err != nil {
		return err
	}
	if len(row.LadderTrace) != 4 || row.LadderTrace[0].Outcome != "no match" || row.LadderTrace[2].Outcome != questions.OutcomeNoOrchestrator {
		return Violatedf("the trace of a question an orchestrator climbed before it stopped reads %+v", row.LadderTrace)
	}
	return nil
}
