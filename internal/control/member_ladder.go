package control

import (
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// MergeQuestion closes a question a member judged the same as one that stays, which is rung 2 worked in an
// orchestrator's place (FR-QD-3). It needs no hold: a hold an orchestrator has on the question ends, and the question's
// lanes move to the one that stays. A planning lane's question goes to the person, as an ask that joins one does, so it
// merges only into a question a person answers: another planning lane's, or one that waits for a person. A merge sent
// again finds the first, which already named the question that stays.
func (q *Questions) MergeQuestion(ctx context.Context, scope ledger.WorkspaceID, id, into string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionMergeQuestion) {
		return workorders.ErrForbidden
	}
	if !ledger.PlainID(into) {
		return fmt.Errorf("%w: the question that stays is named by a plain ID, not %q", questions.ErrInvalid, into)
	}
	if id == into {
		return fmt.Errorf("%w: a question cannot merge into itself", questions.ErrInvalid)
	}
	return q.store.Write(ctx, scope, "question.merge", func(tx ports.WriteTx) error {
		record, err := tx.Questions().Question(id)
		if err != nil {
			return err
		}
		if merged, ok := record.MergedInto(); ok && merged == into {
			return nil
		}
		if !record.Status.Unresolved() {
			return fmt.Errorf("%w: %s is %s", questions.ErrResolved, id, record.Status)
		}
		if record.Direct() {
			target, err := tx.Questions().Question(into)
			if err != nil {
				return err
			}
			if target.Status.Unresolved() && !target.Direct() && target.Status != questions.StatusNeedsHuman {
				return fmt.Errorf("%w: %s is a planning lane's question, which goes to the person, and %s is on the ladder, where an orchestrator could answer it", questions.ErrInvalid, id, into)
			}
		}
		return q.mergeIn(tx, record, into, actor)
	})
}

// DecideNow takes a question off the ladder into the member's own Needs you, in place of the orchestrator that would
// judge it: it waits for a person from now, with a step that says a member chose to, and the batch it joins reaches
// members at once, with whatever else waited in it. A question that already waits for a person is left as it is, so a
// second call finds the first, and one that no longer waits has nothing to decide.
func (q *Questions) DecideNow(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (questions.Question, error) {
	if !authority.Allowed(actor, authority.ActionAnswerQuestion) {
		return questions.Question{}, workorders.ErrForbidden
	}
	var record questions.Question
	err := q.store.Write(ctx, scope, "question.decide_now", func(tx ports.WriteTx) error {
		var err error
		if record, err = tx.Questions().Question(id); err != nil {
			return err
		}
		switch {
		case !record.Status.Unresolved():
			return fmt.Errorf("%w: %s is %s", questions.ErrResolved, id, record.Status)
		case record.Status == questions.StatusNeedsHuman:
			return nil
		}
		record.Status, record.Rung, record.ClaimedBy, record.ClaimUntil = questions.StatusNeedsHuman, questions.RungEscalate, "", 0
		record.LadderTrace = append(record.LadderTrace, questions.Step{Rung: questions.RungEscalate, Outcome: questions.OutcomeDecidedNow, By: actor.ID})
		if err := q.reachPerson(tx, &record, actor); err != nil {
			return err
		}
		batch, err := tx.Questions().Batch(record.BatchID)
		if err != nil {
			return err
		}
		if err := hastenTo(tx, batch, tx.Now(), actor); err != nil {
			return err
		}
		return saveQuestion(tx, record, actor)
	})
	if err != nil {
		return questions.Question{}, err
	}
	if record.BatchID != "" {
		if _, err := q.deliver(ctx, scope, record.BatchID, actor); err != nil {
			return questions.Question{}, err
		}
	}
	return record, nil
}
