package control

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// Ask records a question and returns at once. The same normalized text is the one
// comparison the core makes: an answered question answers, and an open one takes the lane
// as well, so the same question is never asked twice. Everything else waits on the ladder,
// and no call stays open while it climbs.
func (q *Questions) Ask(ctx context.Context, scope ledger.WorkspaceID, ask questions.Ask, actor ledger.Actor) (questions.AskResult, error) {
	draft, err := questions.New(questions.Question{
		ID: ask.ID, Text: ask.Text, Categories: ask.Categories, Blocking: ask.Blocking, Options: ask.Options,
		SubjectKind: ask.SubjectKind, SubjectID: ask.SubjectID, AskedBy: actor.ID,
	})
	if err != nil {
		return questions.AskResult{}, err
	}
	var result questions.AskResult
	err = q.store.Write(ctx, scope, "question.ask", func(tx ports.WriteTx) error {
		var err error
		result, err = q.askIn(tx, draft, ask, actor)
		return err
	})
	if err != nil {
		return questions.AskResult{}, err
	}
	return result, nil
}

func (q *Questions) askIn(tx ports.WriteTx, draft questions.Question, ask questions.Ask, actor ledger.Actor) (questions.AskResult, error) {
	same, err := tx.Questions().ByHash(draft.NormalizedHash)
	if err != nil {
		return questions.AskResult{}, err
	}
	var open *questions.Question
	for i := range same {
		switch {
		case same[i].Status == questions.StatusAnswered:
			answer, usable, err := answerOf(tx, same[i])
			if err != nil {
				return questions.AskResult{}, err
			}
			if usable {
				return answer, nil
			}
		case same[i].Status.Unresolved():
			open = &same[i]
		}
	}
	if open != nil {
		return q.joinAsk(tx, *open, ask, actor)
	}
	if err := checkCategories(tx.Questions(), draft.Categories); err != nil {
		return questions.AskResult{}, err
	}
	if err := insertQuestion(tx, draft, actor); err != nil {
		return questions.AskResult{}, err
	}
	crashpoint.Hit("question.ask", "recorded")
	if err := attachLane(tx, draft.ID, ask, actor); err != nil {
		return questions.AskResult{}, err
	}
	crashpoint.Hit("question.ask", "attached")
	if err := holdBack(tx, draft, actor); err != nil {
		return questions.AskResult{}, err
	}
	return questions.AskResult{Status: questions.AskPending, QuestionID: draft.ID}, nil
}

// answerOf reads what answered a question. A decision a person overturned or a later one
// replaced answers nothing, so the question is asked again.
func answerOf(tx ports.WriteTx, question questions.Question) (questions.AskResult, bool, error) {
	decision, err := tx.Questions().Decision(question.DecisionID)
	if err != nil {
		return questions.AskResult{}, false, err
	}
	if decision.Status != questions.DecisionDecided && decision.Status != questions.DecisionAssumed {
		return questions.AskResult{}, false, nil
	}
	return questions.AskResult{
		Status: questions.AskAnswered, QuestionID: question.ID, DecisionID: decision.ID,
		Answer: decision.Statement, Citations: decision.Citations,
	}, true, nil
}

// joinAsk adds a lane to an open question. A retry of the first ask finds what it wrote and
// reads the same, and a blocking lane makes the question block.
func (q *Questions) joinAsk(tx ports.WriteTx, open questions.Question, ask questions.Ask, actor ledger.Actor) (questions.AskResult, error) {
	if err := attachLane(tx, open.ID, ask, actor); err != nil {
		return questions.AskResult{}, err
	}
	if ask.Blocking && !open.Blocking {
		open.Blocking = true
		if err := saveQuestion(tx, open, actor); err != nil {
			return questions.AskResult{}, err
		}
		if err := holdBack(tx, open, actor); err != nil {
			return questions.AskResult{}, err
		}
		if err := q.hastenFor(tx, open, actor); err != nil {
			return questions.AskResult{}, err
		}
	}
	status := questions.AskAttached
	if open.ID == ask.ID {
		status = questions.AskPending
	}
	return questions.AskResult{Status: status, QuestionID: open.ID}, nil
}

func attachLane(tx ports.WriteTx, question string, ask questions.Ask, actor ledger.Actor) error {
	if ask.Lane == "" {
		return nil
	}
	attachment := questions.Attachment{
		QuestionID: question, Lane: ask.Lane, Generation: ask.Generation, Blocking: ask.Blocking, AttachedAt: tx.Now(),
	}
	added, err := tx.Questions().Attach(attachment)
	if err != nil || !added {
		return err
	}
	event, err := questions.RecordEvent(questions.EventQuestionAttached, questions.SubjectQuestion, question, attachment, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// holdBack sends ready work back to review when a blocking question names it. The review
// stands, because the contract did not change, so the tick returns the work once the
// question closes.
func holdBack(tx ports.WriteTx, question questions.Question, actor ledger.Actor) error {
	if !question.Blocking {
		return nil
	}
	orders, err := heldOrders(tx, question)
	if err != nil {
		return err
	}
	for _, order := range orders {
		if order.State != workorders.Ready {
			continue
		}
		if err := takeEdge(tx, order.ID, workorders.TriggerBackToReview, actor); err != nil {
			return err
		}
	}
	return nil
}

func heldOrders(tx ports.WriteTx, question questions.Question) ([]workorders.WorkOrder, error) {
	switch question.SubjectKind {
	case workorders.SubjectKind:
		order, err := tx.WorkOrders().Get(question.SubjectID)
		if errors.Is(err, ports.ErrNotFound) {
			return nil, nil
		}
		return []workorders.WorkOrder{order}, err
	case planning.SubjectContract:
		return tx.WorkOrders().ByContract(question.SubjectID)
	case planning.SubjectPlan:
		contracts, err := newestContracts(tx.Planning(), question.SubjectID)
		if err != nil {
			return nil, err
		}
		var out []workorders.WorkOrder
		for _, contract := range contracts {
			orders, err := tx.WorkOrders().ByContract(contract.ID)
			if err != nil {
				return nil, err
			}
			out = append(out, orders...)
		}
		return out, nil
	}
	return nil, nil
}

func saveQuestion(tx ports.WriteTx, record questions.Question, actor ledger.Actor) error {
	if err := tx.Questions().Save(record); err != nil {
		return err
	}
	return appendQuestion(tx, record, actor)
}

// Take gives one orchestrator or member the work of a question, with the same
// compare-and-set as a claim, so two never work one question. A holder takes again to keep
// its hold, and a hold past its deadline is free.
func (q *Questions) Take(ctx context.Context, scope ledger.WorkspaceID, id string, ttlMs int64, actor ledger.Actor) (questions.Question, error) {
	if !authority.Allowed(actor, authority.ActionTakeQuestion) {
		return questions.Question{}, workorders.ErrForbidden
	}
	var record questions.Question
	err := q.store.Write(ctx, scope, "question.take", func(tx ports.WriteTx) error {
		won, err := tx.Questions().Take(id, actor.ID, ttlMs)
		if err != nil {
			return err
		}
		if record, err = tx.Questions().Question(id); err != nil {
			return err
		}
		if !won {
			if !record.Status.Unresolved() {
				return fmt.Errorf("%w: %s", questions.ErrResolved, record.Status)
			}
			return fmt.Errorf("%w: %s holds %s", questions.ErrTaken, record.ClaimedBy, id)
		}
		crashpoint.Hit("question.take", "taken")
		return appendQuestion(tx, record, actor)
	})
	if err != nil {
		return questions.Question{}, err
	}
	return record, nil
}

// held reads a question the caller may work: unresolved, and held by it with time left.
func held(tx ports.WriteTx, id string, actor ledger.Actor) (questions.Question, error) {
	record, err := tx.Questions().Question(id)
	if err != nil {
		return questions.Question{}, err
	}
	if !record.Status.Unresolved() {
		return questions.Question{}, fmt.Errorf("%w: %s", questions.ErrResolved, record.Status)
	}
	if record.ClaimedBy != actor.ID || record.ClaimUntil <= tx.Now() {
		return questions.Question{}, fmt.Errorf("%w: %s", questions.ErrNotHeld, id)
	}
	return record, nil
}

// Climb records one rung a question passed, with the candidates considered, and moves it to
// the next. A one-way category cannot stand on rungs 3 and 4.
func (q *Questions) Climb(ctx context.Context, scope ledger.WorkspaceID, id string, step questions.Step, actor ledger.Actor) (questions.Question, error) {
	var record questions.Question
	err := q.store.Write(ctx, scope, "question.climb", func(tx ports.WriteTx) error {
		var err error
		if record, err = held(tx, id, actor); err != nil {
			return err
		}
		policy, err := policyInForce(tx)
		if err != nil {
			return err
		}
		if (step.Rung == questions.RungDecide || step.Rung == questions.RungAssume) && !policy.MayDecide(record) {
			return fmt.Errorf("%w: %v", questions.ErrOneWay, record.Categories)
		}
		if step.Rung != record.Rung {
			return fmt.Errorf("%w: step %d on rung %d", questions.ErrWrongRung, step.Rung, record.Rung)
		}
		record.LadderTrace = append(record.LadderTrace, step)
		record.Rung = policy.NextRung(record)
		crashpoint.Hit("question.climb", "stepped")
		return saveQuestion(tx, record, actor)
	})
	if err != nil {
		return questions.Question{}, err
	}
	return record, nil
}

// Escalate takes a question to the gate, and past it to a human. A refusal changes nothing,
// and it names everything the question lacks (FR-QD-3).
func (q *Questions) Escalate(ctx context.Context, scope ledger.WorkspaceID, id string, escalation questions.Escalation, actor ledger.Actor) (questions.Question, error) {
	var record questions.Question
	err := q.store.Write(ctx, scope, "question.escalate", func(tx ports.WriteTx) error {
		var err error
		if record, err = held(tx, id, actor); err != nil {
			return err
		}
		record.EscalationReason, record.Recommendation = escalation.Reason, escalation.Recommendation
		if len(escalation.Options) != 0 {
			record.Options = escalation.Options
		}
		if len(escalation.Categories) != 0 {
			if err := checkCategories(tx.Questions(), escalation.Categories); err != nil {
				return err
			}
			record.Categories = escalation.Categories
		}
		if failures := questions.Gate(record); len(failures) != 0 {
			return &questions.GateError{Failures: failures}
		}
		crashpoint.Hit("question.escalate", "passed")
		record.Status, record.Rung, record.ClaimedBy, record.ClaimUntil = questions.StatusNeedsHuman, questions.RungEscalate, "", 0
		if err := q.gather(tx, &record, actor); err != nil {
			return err
		}
		return saveQuestion(tx, record, actor)
	})
	if err != nil {
		return questions.Question{}, err
	}
	return record, nil
}

// Resolve answers a question from a decision the ledger holds, which is rung 1 when an agent
// judges that a candidate answers it.
func (q *Questions) Resolve(ctx context.Context, scope ledger.WorkspaceID, id, decision string, actor ledger.Actor) (questions.Question, error) {
	var record questions.Question
	err := q.store.Write(ctx, scope, "question.resolve", func(tx ports.WriteTx) error {
		var err error
		if record, err = held(tx, id, actor); err != nil {
			return err
		}
		answer, err := tx.Questions().Decision(decision)
		if err != nil {
			return err
		}
		if err := checkAnswers(tx, answer, record, actor); err != nil {
			return err
		}
		crashpoint.Hit("question.resolve", "answered")
		record.Status, record.DecisionID, record.ClaimedBy, record.ClaimUntil = questions.StatusAnswered, decision, "", 0
		return q.closeQuestion(tx, record, actor)
	})
	if err != nil {
		return questions.Question{}, err
	}
	return record, nil
}

// checkAnswers holds a rung 1 answer to what the policy lets an orchestrator rely on. It
// never decides a one-way question, so only a decision in a forced category answers one, and
// no orchestrator can have recorded that under this policy.
func checkAnswers(tx ports.WriteTx, answer questions.Decision, question questions.Question, actor ledger.Actor) error {
	if !answer.Status.InForce() {
		return fmt.Errorf("%w: %s is %s", questions.ErrDecisionEnded, answer.ID, answer.Status)
	}
	if authority.Allowed(actor, authority.ActionDecideUnbound) {
		return nil
	}
	policy, err := policyInForce(tx)
	if err != nil {
		return err
	}
	if !policy.MayDecide(question) && policy.Open(answer.Categories) {
		return fmt.Errorf("%w: %s is not a person's answer to %v", questions.ErrOneWay, answer.ID, question.Categories)
	}
	return nil
}

// Merge closes a question an agent judged the same as an open one, which is rung 2 for a
// paraphrase the core cannot see. Its lanes move to the question that stays.
func (q *Questions) Merge(ctx context.Context, scope ledger.WorkspaceID, id, into string, actor ledger.Actor) error {
	if id == into {
		return fmt.Errorf("%w: a question cannot merge into itself", questions.ErrInvalid)
	}
	return q.store.Write(ctx, scope, "question.merge", func(tx ports.WriteTx) error {
		record, err := held(tx, id, actor)
		if err != nil {
			return err
		}
		target, err := tx.Questions().Question(into)
		if err != nil {
			return err
		}
		if !target.Status.Unresolved() {
			return fmt.Errorf("%w: %s is %s", questions.ErrResolved, into, target.Status)
		}
		attached, err := tx.Questions().Attachments(id)
		if err != nil {
			return err
		}
		for _, attachment := range attached {
			moved := questions.Ask{Lane: attachment.Lane, Generation: attachment.Generation, Blocking: attachment.Blocking}
			if err := attachLane(tx, into, moved, actor); err != nil {
				return err
			}
		}
		crashpoint.Hit("question.merge", "moved")
		record.Status, record.ClaimedBy, record.ClaimUntil = questions.StatusDismissed, "", 0
		record.LadderTrace = append(record.LadderTrace, questions.Step{Rung: questions.RungOpen, Candidates: []string{into}, Outcome: questions.OutcomeMerged})
		if err := saveQuestion(tx, record, actor); err != nil {
			return err
		}
		if !record.Blocking || target.Blocking {
			return nil
		}
		target.Blocking = true
		if err := saveQuestion(tx, target, actor); err != nil {
			return err
		}
		if err := holdBack(tx, target, actor); err != nil {
			return err
		}
		return q.hastenFor(tx, target, actor)
	})
}
