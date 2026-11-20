package control

import (
	"errors"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

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
