package control

import (
	"errors"
	"strings"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The first words of the reason a plan that is not finished goes back with. Ropeshift's own send-back says it did
// it, and the reason a card offers a member says only what is missing. Each is how the other tells which of the two
// a work order's newest note is.
const (
	unfinishedSentBack = "Ropeshift sent this plan back because it is not finished:"
	unfinishedMissing  = "The plan is not finished:"
)

func unfinishedReason(lead string, missing []string) string {
	var out strings.Builder
	out.WriteString(lead)
	for _, line := range missing {
		out.WriteString("\n- " + line)
	}
	return out.String()
}

// holdPlanBack handles a planning branch whose plan is not finished, which is never offered for approval. The first time,
// ropeshift sends the plan work order back itself, with what is missing as the reason, as a system event, so the next
// lane finishes the plan. When that did not help, or the work order has no attempt left, it keeps what is missing on the
// work order, which its card in Needs you shows beside Send back. A work order is handled once for an attempt.
func holdPlanBack(tx ports.WriteTx, workOrder string, missing []string) error {
	current, err := tx.WorkOrders().Get(workOrder)
	if err != nil {
		return err
	}
	if current.State != workorders.Acceptance {
		return nil
	}
	note, err := tx.Evidence().Reworked(workOrder)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	found := err == nil
	if found && note.Attempt == current.Fence && strings.HasPrefix(note.Reason, unfinishedMissing) {
		return nil
	}
	if !found || !strings.HasPrefix(note.Reason, unfinishedSentBack) {
		attempts, err := defaultAttempts(tx.Policies())
		if err != nil {
			return err
		}
		if current.AttemptsLeft(attempts) {
			_, err := reworkFor(tx, workOrder, workorders.ReworkNote{Reason: unfinishedReason(unfinishedSentBack, missing)}, nil, intakeActor, func() {})
			return err
		}
	}
	reason := unfinishedReason(unfinishedMissing, missing)
	if err := tx.Evidence().RecordRework(workorders.ReworkNote{WorkOrder: workOrder, Attempt: current.Fence, Reason: reason}); err != nil {
		return err
	}
	// The note moves no state, so the event says so: it changes nothing a replay reads, and the ledger records why a
	// member was called.
	event, err := workorders.ChangeEvent(workorders.EventEvidenceRecorded, workOrder, workorders.Change{Reason: reason}, intakeActor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// UnfinishedOf is what a plan work order waiting for acceptance lacks, and the reason that sends it back, when
// ropeshift held its plan back and sent it back already. A work order with no such note has none.
func UnfinishedOf(evidence ports.EvidenceReader, order workorders.WorkOrder) ([]string, string, error) {
	if order.Kind != workorders.KindPlan || order.State != workorders.Acceptance {
		return nil, "", nil
	}
	note, err := evidence.Reworked(order.ID)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, "", nil
	}
	if err != nil || note.Attempt != order.Fence || !strings.HasPrefix(note.Reason, unfinishedMissing) {
		return nil, "", err
	}
	var missing []string
	for _, line := range strings.Split(note.Reason, "\n") {
		if item, ok := strings.CutPrefix(line, "- "); ok {
			missing = append(missing, item)
		}
	}
	return missing, note.Reason, nil
}

// branchFinish reads what a branch's plan holds that says whether it is finished.
func branchFinish(batch ports.ChangeBatch, plan ports.RecordItem, goal *planning.DetailGoal) planning.Finish {
	finish := planning.Finish{
		Title: plan.Plan.Title, GoalNamed: plan.Plan.Goal != "", Goal: goal,
		Documents: len(plan.Plan.Documents), Decisions: len(plan.Plan.Decisions), Body: plan.Statement,
	}
	for _, item := range batch.Items {
		if item.Contract.Plan == plan.Plan.ID {
			finish.Contracts = append(finish.Contracts, planning.FinishContract{
				ID: item.Contract.ID, Statement: item.Statement, WorkOrders: len(item.Contract.WorkOrders), Existing: item.Contract.Existing,
			})
		}
	}
	return finish
}

// versionFinish reads the same of a version the ledger holds.
func versionFinish(tx ports.WriteTx, version planning.PlanVersion) (planning.Finish, error) {
	record, err := tx.Planning().Plan(version.PlanID)
	if err != nil {
		return planning.Finish{}, err
	}
	finish := planning.Finish{
		Title: record.Title, GoalNamed: record.GoalID != "",
		Documents: len(version.Documents), Decisions: len(version.Decisions), Body: version.Body,
	}
	if record.GoalID != "" {
		goal, err := tx.Planning().Goal(record.GoalID)
		switch {
		case err == nil:
			finish.Goal = &planning.DetailGoal{Title: goal.Title, Statement: goal.Statement, SuccessMeasures: goal.SuccessMeasures}
		case !errors.Is(err, ports.ErrNotFound):
			return planning.Finish{}, err
		}
	}
	contracts, err := newestContracts(tx.Planning(), version.PlanID)
	if err != nil {
		return planning.Finish{}, err
	}
	for _, contract := range contracts {
		finish.Contracts = append(finish.Contracts, planning.FinishContract{
			ID: contract.ID, Statement: contract.Statement, WorkOrders: len(contract.Orders), Existing: contract.Existing,
		})
	}
	return finish, nil
}
