package control

import (
	"errors"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// askLeftFiles takes each path a lane left out to the orchestrator (FR-LANE-20). A path whose question is open, or
// was answered by a decision that stands, raises none: that is the ladder's first rung, read from the record. A path
// that cannot make a question, and a lane whose work names no repository, raise nothing, because a report that
// failed would be read again for good.
func askLeftFiles(tx ports.WriteTx, worker, report string, e ledger.UploadEvent, guard ledger.LaneGuard) error {
	if len(guard.LeftOut) == 0 || e.WorkOrder == "" {
		return nil
	}
	order, err := tx.WorkOrders().Get(e.WorkOrder)
	if errors.Is(err, ports.ErrNotFound) || err == nil && order.RepositoryID == "" {
		return nil
	}
	if err != nil {
		return err
	}
	actor := ledger.Actor{Kind: ledger.ActorWorker, ID: worker}
	for _, path := range guard.LeftOut {
		draft, err := questions.LeftFile(questions.LeftFileID(report, path), order.RepositoryID, path)
		if err != nil {
			continue
		}
		draft.AskedBy = worker
		if err := askLeftFile(tx, draft, actor); err != nil {
			return err
		}
	}
	return nil
}

// askLeftFile records one path's question unless the record already answers it or holds it open.
func askLeftFile(tx ports.WriteTx, draft questions.Question, actor ledger.Actor) error {
	if _, err := tx.Questions().Question(draft.ID); err == nil || !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	same, err := tx.Questions().ByHash(draft.NormalizedHash)
	if err != nil {
		return err
	}
	for _, question := range same {
		if question.Status.Unresolved() {
			return nil
		}
		if question.Status != questions.StatusAnswered {
			continue
		}
		if _, usable, err := answerOf(tx, question, false); err != nil || usable {
			return err
		}
	}
	return insertQuestion(tx, draft, actor)
}
