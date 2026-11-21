package control

import (
	"errors"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// maxLeftQuestions is how many paths of one report raise questions, whatever a report says it left out.
const maxLeftQuestions = 100

// askLeftFiles takes each path a lane left out to the orchestrator (FR-LANE-20), and returns the paths it could not
// make a question of, which the lane's event names. A path whose question is open, was dismissed, or was answered by
// a decision that stands raises none: that is the ladder's first rung, read from the record. A lane whose work names
// no repository raises nothing, because a report that failed would be read again for good.
func askLeftFiles(tx ports.WriteTx, worker, report string, e ledger.UploadEvent, guard ledger.LaneGuard) ([]string, error) {
	if len(guard.LeftOut) == 0 || e.WorkOrder == "" {
		return nil, nil
	}
	order, err := tx.WorkOrders().Get(e.WorkOrder)
	if errors.Is(err, ports.ErrNotFound) || err == nil && order.RepositoryID == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	actor := ledger.Actor{Kind: ledger.ActorWorker, ID: worker}
	paths := guard.LeftOut
	if len(paths) > maxLeftQuestions {
		paths = paths[:maxLeftQuestions]
	}
	var unasked []string
	for _, path := range paths {
		draft, err := questions.LeftFile(questions.LeftFileID(report, path), order.RepositoryID, path)
		if err != nil {
			unasked = append(unasked, path)
			continue
		}
		draft.AskedBy = worker
		if err := askLeftFile(tx, draft, actor); err != nil {
			return nil, err
		}
	}
	return unasked, nil
}

// askLeftFile records one path's question unless the record already holds the same one: open, dismissed, or
// answered by a decision that stands. The same one has the same text exactly, because the hash the record finds it by
// folds case and spacing, and two paths can differ in only those.
func askLeftFile(tx ports.WriteTx, draft questions.Question, actor ledger.Actor) error {
	if _, err := tx.Questions().Question(draft.ID); err == nil || !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	same, err := tx.Questions().ByHash(draft.NormalizedHash)
	if err != nil {
		return err
	}
	for _, question := range same {
		switch {
		case question.Text != draft.Text:
		case question.Status.Unresolved(), question.Status == questions.StatusDismissed:
			return nil
		case question.Status == questions.StatusAnswered:
			if _, usable, err := answerOf(tx, question, false); err != nil || usable {
				return err
			}
		}
	}
	return insertQuestion(tx, draft, actor)
}
