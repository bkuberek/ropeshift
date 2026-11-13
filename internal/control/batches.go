package control

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// gather puts a question that passed the gate into the batch that is open, or opens one.
// All of it uses database time, because a batch's close is a timer.
func (q *Questions) gather(tx ports.WriteTx, record *questions.Question, actor ledger.Actor) error {
	batch, err := tx.Questions().OpenBatch()
	if errors.Is(err, ports.ErrNotFound) {
		batch, err = q.openBatch(tx, actor)
	}
	if err != nil {
		return err
	}
	record.BatchID = batch.ID
	crashpoint.Hit("question.escalate", "gathered")
	if !record.Blocking {
		return nil
	}
	return q.hasten(tx, batch, actor)
}

func (q *Questions) openBatch(tx ports.WriteTx, actor ledger.Actor) (questions.Batch, error) {
	id, err := newID(tx.Now())
	if err != nil {
		return questions.Batch{}, err
	}
	batch := questions.Batch{ID: id, Status: questions.BatchOpen, OpenedAt: tx.Now(), ClosesAt: tx.Now() + q.batches.WindowMs}
	if err := tx.Questions().InsertBatch(batch); err != nil {
		return questions.Batch{}, err
	}
	return batch, appendBatch(tx, batch, actor)
}

// hasten brings a batch's close within what a blocking question may wait. A close that is
// already sooner stays, so the rule only ever moves it earlier.
func (q *Questions) hasten(tx ports.WriteTx, batch questions.Batch, actor ledger.Actor) error {
	return hastenTo(tx, batch, tx.Now()+q.batches.MaxWaitMs, actor)
}

func hastenTo(tx ports.WriteTx, batch questions.Batch, limit int64, actor ledger.Actor) error {
	moved, err := tx.Questions().HastenBatch(batch.ID, limit)
	if err != nil || !moved {
		return err
	}
	batch.ClosesAt = limit
	return appendBatch(tx, batch, actor)
}

// toPerson sends a drafted question to the person with no ladder, and joins it to the open batch,
// which it closes now. A planning lane's person is present, so the question does not wait for a window
// or for the time a blocking one may wait, and the next tick delivers the batch.
func (q *Questions) toPerson(tx ports.WriteTx, draft questions.Question, actor ledger.Actor) (questions.Question, error) {
	record, err := questions.ToPerson(draft)
	if err != nil {
		return questions.Question{}, err
	}
	if err := q.gather(tx, &record, actor); err != nil {
		return questions.Question{}, err
	}
	batch, err := tx.Questions().Batch(record.BatchID)
	if err != nil {
		return questions.Question{}, err
	}
	return record, hastenTo(tx, batch, tx.Now(), actor)
}

// hastenFor applies that rule to a question that started blocking after it was gathered. A
// question in no open batch has nothing to hurry.
func (q *Questions) hastenFor(tx ports.WriteTx, record questions.Question, actor ledger.Actor) error {
	if record.BatchID == "" {
		return nil
	}
	batch, err := tx.Questions().Batch(record.BatchID)
	if err != nil || batch.Status != questions.BatchOpen {
		return err
	}
	return q.hasten(tx, batch, actor)
}

func appendBatch(tx ports.WriteTx, batch questions.Batch, actor ledger.Actor) error {
	event, err := questions.RecordEvent(questions.EventBatchChanged, questions.SubjectBatch, batch.ID, batch, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// DeliverBatches ends every batch whose window closed, and returns those that reached
// members. It holds no state, so a tick that dies is made up by the next one.
func (q *Questions) DeliverBatches(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error) {
	var due []string
	err := q.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		due, err = tx.Questions().DueBatches(now)
		return err
	})
	if err != nil {
		return nil, err
	}
	var delivered []string
	var failed []error
	for _, id := range due {
		reached, err := q.deliver(ctx, scope, id, actor)
		if err != nil {
			failed = append(failed, err)
		}
		if reached {
			delivered = append(delivered, id)
		}
	}
	return delivered, errors.Join(failed...)
}

// deliver closes one batch. The compare-and-set decides it, so racing ticks deliver once,
// and one event says so: that event is the one interruption a batch counts as. A batch
// whose questions were all answered in the meantime interrupts nobody.
func (q *Questions) deliver(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (bool, error) {
	reached := false
	err := q.store.Write(ctx, scope, "question.deliver", func(tx ports.WriteTx) error {
		rows, err := tx.Questions().BatchQuestions(id)
		if err != nil {
			return err
		}
		var waiting []string
		for _, row := range rows {
			if row.Status == questions.StatusNeedsHuman {
				waiting = append(waiting, row.ID)
			}
		}
		status, deliveredAt := questions.BatchDelivered, tx.Now()
		if len(waiting) == 0 {
			status, deliveredAt = questions.BatchEmpty, 0
		}
		moved, err := tx.Questions().CloseBatch(id, status, deliveredAt)
		if err != nil || !moved {
			return err
		}
		crashpoint.Hit("question.deliver", "closed")
		batch, err := tx.Questions().Batch(id)
		if err != nil {
			return err
		}
		if len(waiting) == 0 {
			return appendBatch(tx, batch, actor)
		}
		reached = true
		return appendDelivery(tx, questions.Delivery{Batch: batch, Questions: waiting}, actor)
	})
	return reached, err
}

func appendDelivery(tx ports.WriteTx, delivery questions.Delivery, actor ledger.Actor) error {
	event, err := questions.RecordEvent(questions.EventBatchDelivered, questions.SubjectBatch, delivery.Batch.ID, delivery, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// Answer records a member's answer as a decision and closes the question, in one
// transaction, so an answer stands or it does not. It takes any question that still waits:
// nothing has to climb the ladder, because no feature may need an orchestrator (D25), and a
// member's answer ends an orchestrator's hold. A question is answered once, and the
// client's ID lets a retry find that answer.
func (q *Questions) Answer(ctx context.Context, scope ledger.WorkspaceID, id string, answer questions.Answer, actor ledger.Actor) (questions.Decision, error) {
	return q.AnswerFrom(ctx, scope, id, answer, actor, "")
}

// AnswerFrom is Answer with where the member's word came from, which the decision's event keeps: the web
// listener, the command line, or the conversation the question showed in. Empty records none.
func (q *Questions) AnswerFrom(ctx context.Context, scope ledger.WorkspaceID, id string, answer questions.Answer, actor ledger.Actor, channel string) (questions.Decision, error) {
	if !authority.Allowed(actor, authority.ActionAnswerQuestion) {
		return questions.Decision{}, workorders.ErrForbidden
	}
	if err := answer.Check(); err != nil {
		return questions.Decision{}, err
	}
	record, err := questions.NewDecision(questions.Decision{
		ID: answer.DecisionID, Status: questions.DecisionDecided, Statement: answer.Statement, Rationale: answer.Rationale,
		Citations: answer.Citations, QuestionID: id, DecidedBy: actor.ID,
	})
	if err != nil {
		return questions.Decision{}, err
	}
	err = q.store.Write(ctx, scope, "question.answer", func(tx ports.WriteTx) error {
		question, err := tx.Questions().Question(id)
		if err != nil {
			return err
		}
		if question.DecisionID == record.ID && question.Status == questions.StatusAnswered {
			record, err = tx.Questions().Decision(record.ID)
			return err
		}
		if !question.Status.Unresolved() {
			return fmt.Errorf("%w: %s", questions.ErrResolved, question.Status)
		}
		policy, err := policyInForce(tx)
		if err != nil {
			return err
		}
		record.Categories, record.Scope = question.Categories, question.SubjectID
		if question.EscalationReason == questions.ReasonOneWay || !policy.MayDecide(question) {
			record.Reversibility = questions.OneWay
		}
		if err := admit(tx, &record, policy, actor); err != nil {
			return err
		}
		if err := insertDecisionFrom(tx, record, actor, channel); err != nil {
			return err
		}
		if err := q.noticeOneWay(tx, record, actor); err != nil {
			return err
		}
		crashpoint.Hit("question.answer", "decided")
		question.Status, question.DecisionID, question.ClaimedBy, question.ClaimUntil = questions.StatusAnswered, record.ID, "", 0
		return q.closeQuestion(tx, question, actor)
	})
	if err != nil {
		return questions.Decision{}, err
	}
	return record, nil
}
