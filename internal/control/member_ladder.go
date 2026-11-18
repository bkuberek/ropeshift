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
// lanes move to the one that stays.
func (q *Questions) MergeQuestion(ctx context.Context, scope ledger.WorkspaceID, id, into string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionMergeQuestion) {
		return workorders.ErrForbidden
	}
	if id == into {
		return fmt.Errorf("%w: a question cannot merge into itself", questions.ErrInvalid)
	}
	return q.store.Write(ctx, scope, "question.merge", func(tx ports.WriteTx) error {
		record, err := tx.Questions().Question(id)
		if err != nil {
			return err
		}
		if !record.Status.Unresolved() {
			return fmt.Errorf("%w: %s is %s", questions.ErrResolved, id, record.Status)
		}
		return q.mergeIn(tx, record, into, actor)
	})
}
