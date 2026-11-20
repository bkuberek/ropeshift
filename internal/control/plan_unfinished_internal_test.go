package control

import (
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

func finishBatch(title, goalTitle, prose string, orders ...planning.WorkOrderSpec) (ports.ChangeBatch, ports.RecordItem, *planning.DetailGoal) {
	plan := ports.RecordItem{Plan: &planning.PlanFile{Kind: planning.FilePlan, ID: "p", Title: title, Goal: "g"}, Statement: prose}
	batch := ports.ChangeBatch{Records: []ports.RecordItem{plan}}
	batch.Items = append(batch.Items, ports.WorkItem{
		Contract:  planning.ContractFile{Kind: planning.FileContract, ID: "c", Plan: "p", WorkOrders: orders},
		Statement: "Serve the endpoints.",
	})
	return batch, plan, &planning.DetailGoal{Title: goalTitle, SuccessMeasures: []string{"Support requests fall by half"}}
}

func TestABranchPlanThatIsStillTheTemplateIsUnfinished(t *testing.T) {
	batch, plan, goal := finishBatch(planning.PlaceholderPlanTitle, planning.PlaceholderGoalTitle, planning.PlaceholderPlanProse)
	if got := planning.Unfinished(branchFinish(batch, plan, goal)); len(got) != 3 {
		t.Fatalf("a template plan reads as %v", got)
	}
}

func TestABranchPlanThatIsWrittenIsFinished(t *testing.T) {
	batch, plan, goal := finishBatch("Accounts", "Customers manage accounts", "Build the API, then the page.", orderSpec("w1"))
	if got := planning.Unfinished(branchFinish(batch, plan, goal)); len(got) != 0 {
		t.Fatalf("a written plan reads as unfinished: %v", got)
	}
	finish := branchFinish(batch, plan, goal)
	if len(finish.Contracts) != 1 || finish.Contracts[0].WorkOrders != 1 || !finish.GoalNamed {
		t.Errorf("the branch's plan reads as %+v", finish)
	}
}
