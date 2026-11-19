package server

import (
	"errors"
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// selectQuestions reads the questions a questions view asks for (FR-QD-13): the open ones at every rung by default,
// the questions of one status, the merged ones, or every one, narrowed to a lane's or a work order's when the address
// names one. A dismissed question that a merge closed reads as merged, and not as dismissed.
func selectQuestions(tx ports.ReadTx, r *http.Request) ([]questions.Question, error) {
	filter := filterOf(r)
	keep := func(q questions.Question) bool { return q.Status.Unresolved() }
	switch filter.Status {
	case "":
	case api.QuestionStatusAll:
		filter.Status, keep = "", func(questions.Question) bool { return true }
	case api.QuestionStatusMerged:
		filter.Status, keep = string(questions.StatusDismissed), merged
	case string(questions.StatusDismissed):
		keep = func(q questions.Question) bool { return !merged(q) }
	default:
		keep = func(questions.Question) bool { return true }
	}
	query := r.URL.Query()
	if lane, order := query.Get("lane"), query.Get("work_order"); lane != "" || order != "" {
		asked, err := askedFrom(tx, lane, order)
		if err != nil {
			return nil, err
		}
		status := keep
		keep = func(q questions.Question) bool { return status(q) && asked(q) }
	}
	return questionsWhere(tx, filter, keep)
}

func merged(q questions.Question) bool {
	_, ok := q.MergedInto()
	return ok
}

// askedFrom says whether a question is a lane's, or a work order's: one the lane asked or is attached to, and for a
// work order one about it or one any of its lanes asked or is attached to. Both narrow when both are named.
func askedFrom(tx ports.ReadTx, lane, order string) (func(questions.Question) bool, error) {
	lanes := map[string]bool{}
	if order != "" {
		rows, err := tx.Lanes().Rows()
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.WorkOrder == order {
				lanes[row.ID] = true
			}
		}
	}
	attachments, err := tx.Questions().Attachments("")
	if err != nil {
		return nil, err
	}
	attached := map[string]map[string]bool{}
	for _, attachment := range attachments {
		if attached[attachment.QuestionID] == nil {
			attached[attachment.QuestionID] = map[string]bool{}
		}
		attached[attachment.QuestionID][attachment.Lane] = true
	}
	return func(q questions.Question) bool {
		asks := func(id string) bool { return q.AskedBy == id || attached[q.ID][id] }
		if lane != "" && !asks(lane) {
			return false
		}
		if order == "" {
			return true
		}
		if q.SubjectKind == workorders.SubjectKind && q.SubjectID == order {
			return true
		}
		for id := range lanes {
			if asks(id) {
				return true
			}
		}
		return false
	}, nil
}

// ladderReads is what a read of questions needs to show each one's whole ladder, read once for every question it
// answers: database time, the name of each worker, every lane attached to a question, and every batch.
type ladderReads struct {
	now      int64
	machines map[string]string
	attached map[string][]questions.Attachment
	batches  map[string]questions.Batch
}

func readLadder(tx ports.ReadTx) (ladderReads, error) {
	now, err := tx.Now()
	if err != nil {
		return ladderReads{}, err
	}
	workers, err := tx.Workers().List()
	if err != nil {
		return ladderReads{}, err
	}
	reads := ladderReads{now: now, machines: map[string]string{}, attached: map[string][]questions.Attachment{}, batches: map[string]questions.Batch{}}
	for _, worker := range workers {
		reads.machines[worker.ID] = worker.Name
	}
	attachments, err := tx.Questions().Attachments("")
	if err != nil {
		return ladderReads{}, err
	}
	for _, attachment := range attachments {
		reads.attached[attachment.QuestionID] = append(reads.attached[attachment.QuestionID], attachment)
	}
	batches, err := tx.Questions().Batches()
	if err != nil {
		return ladderReads{}, err
	}
	for _, batch := range batches {
		reads.batches[batch.ID] = batch
	}
	return reads, nil
}

// fill adds to a question's view who holds it while the hold lasts, the lanes attached to it, its batch, what closed
// it, and the machine of each orchestrator its view names.
func (l ladderReads) fill(tx ports.ReadTx, view *api.QuestionView) error {
	row := view.Question
	if row.ClaimedBy != "" && row.ClaimUntil > l.now {
		view.Holder = &api.QuestionHolder{ID: row.ClaimedBy, Until: row.ClaimUntil}
		if machine, ok := l.machines[row.ClaimedBy]; ok {
			view.Holder.Orchestrator, view.Holder.Machine = ledger.OrchestratorOf(row.ClaimedBy), machine
		}
	}
	view.Attached = l.attached[row.ID]
	if batch, ok := l.batches[row.BatchID]; ok && row.BatchID != "" {
		view.Batch = &batch
	}
	if row.Status == questions.StatusAnswered && row.DecisionID != "" {
		decision, err := tx.Questions().Decision(row.DecisionID)
		switch {
		case err == nil:
			view.ClosedBy = &decision
		case !errors.Is(err, ports.ErrNotFound):
			return err
		}
	}
	view.MergedInto, _ = row.MergedInto()
	var named []string
	if view.Holder != nil {
		named = append(named, view.Holder.ID)
	}
	for _, step := range row.LadderTrace {
		named = append(named, step.By)
	}
	if view.ClosedBy != nil {
		named = append(named, view.ClosedBy.DecidedBy)
	}
	for _, id := range named {
		if machine, ok := l.machines[id]; ok {
			if view.Machines == nil {
				view.Machines = map[string]string{}
			}
			view.Machines[id] = machine
		}
	}
	return nil
}
