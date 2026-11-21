package control

import (
	"errors"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// maxLeftQuestions is how many paths of one report raise questions, whatever a report says it left out.
const maxLeftQuestions = 100

// askLeftFiles takes each path a lane left out, or held in commits the worker refused to push, to the orchestrator
// (FR-LANE-20), and returns the paths it could not make a question of, which the lane's event names. A path whose
// question is open, was dismissed, or was answered by a decision that stands raises none: that is the ladder's first
// rung, read from the record. A lane whose work names no repository raises nothing, because a report that failed
// would be read again for good.
func askLeftFiles(tx ports.WriteTx, worker, report string, e ledger.UploadEvent, guard ledger.LaneGuard) ([]string, error) {
	paths := slices.Concat(guard.LeftOut, guard.Refused)
	if len(paths) == 0 || e.WorkOrder == "" {
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

// desiredFiles is what the orchestrator decided of the paths lanes left out in one repository, from the decisions in
// force in the files category scoped to it, and nil when it decided none. A decision whose statement is neither
// option of its question decides nothing a worker acts on.
func desiredFiles(tx ports.ReadTx, repository string) (*ledger.DesiredFiles, error) {
	if repository == "" {
		return nil, nil
	}
	filter := ports.RecordFilter{
		Category: questions.CategoryFiles, Scope: questions.Scope{Kind: questions.ScopeRepository, ID: repository}.String(),
		Limit: ports.MaxRecords,
	}
	var files ledger.DesiredFiles
	for {
		decisions, err := tx.Questions().Decisions(filter)
		if err != nil {
			return nil, err
		}
		for _, decision := range decisions {
			if err := decidedFile(tx, decision, &files); err != nil {
				return nil, err
			}
		}
		if len(decisions) < filter.Limit {
			break
		}
		filter.After = decisions[len(decisions)-1].ID
	}
	if len(files.Ignore) == 0 && len(files.Keep) == 0 {
		return nil, nil
	}
	slices.Sort(files.Ignore)
	slices.Sort(files.Keep)
	return &files, nil
}

func decidedFile(tx ports.ReadTx, decision questions.Decision, files *ledger.DesiredFiles) error {
	if !decision.Status.InForce() || decision.QuestionID == "" {
		return nil
	}
	question, err := tx.Questions().Question(decision.QuestionID)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	path, ours, err := leftFileOf(tx.Workers(), question)
	if err != nil || !ours {
		return err
	}
	switch choice, _ := questions.LeftFileChoice(question, decision.Statement); choice {
	case questions.LeftFileIgnore:
		if questions.IgnorablePath(path) {
			files.Ignore = append(files.Ignore, path)
		}
	case questions.LeftFileKeep:
		files.Keep = append(files.Keep, path)
	}
	return nil
}

// leftFileOf reads the path of a question askLeftFiles made, and says whether it is one: its ID is one only the
// control plane gives (questions.CheckAskedID), and a worker this ledger holds asked it. A question in the files
// category that a lane or a member asked names no path a worker acts on, whatever its text says.
func leftFileOf(workers ports.WorkerReader, question questions.Question) (string, bool, error) {
	_, path, ok := questions.ReadLeftFile(question)
	if !ok {
		return "", false, nil
	}
	if _, err := workers.Find(question.AskedBy); errors.Is(err, ports.ErrNotFound) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	return path, true, nil
}

// leftFilePath is the path a decision on a left-out path's question decides, and empty for any other decision.
func leftFilePath(reader ports.QuestionReader, decision questions.Decision) (string, error) {
	if decision.QuestionID == "" || !slices.Contains(decision.Categories, questions.CategoryFiles) {
		return "", nil
	}
	question, err := reader.Question(decision.QuestionID)
	if errors.Is(err, ports.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	_, path, _ := questions.ReadLeftFile(question)
	return path, nil
}
