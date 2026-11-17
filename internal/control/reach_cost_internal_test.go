package control

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// reachCostBound is what computing one decision's reach may cost on the larger ledger of the test below. It
// sits well above what a race-enabled run on a busy machine measures, so it fails on a change in how the cost
// grows, not on a slow machine.
const reachCostBound = 2 * time.Second

// TestADecisionsReachStaysCheapOnALargeLedger measures what computing a decision's reach costs: a plan scope
// over finished changes under 50 contracts, each change with a review that waits, so the reach reads every
// work order, resolves the plan of each contract version once, and finds the review of each change it binds. It
// measures two sizes, so the log shows how the cost grows.
func TestADecisionsReachStaysCheapOnALargeLedger(t *testing.T) {
	const fewer, more = 1000, 2000
	small, large := reachCost(t, fewer), reachCost(t, more)
	t.Logf("reach of a plan decision: %v over %d work orders, %v over %d", small, 2*fewer, large, 2*more)
	if large > reachCostBound {
		t.Errorf("the reach over %d work orders took %v, over the bound of %v", 2*more, large, reachCostBound)
	}
}

// reachCost puts changes, each with its review, under one plan, and returns the fastest of three computations
// of the reach of a decision on that plan. It writes the rows the reach reads directly, with an event for each
// contract version and none for each work order, which the reach never reads.
func reachCost(t *testing.T, changes int) time.Duration {
	t.Helper()
	const scope = ledger.WorkspaceID("reach")
	actor := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	store, err := fake.NewMigratedStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateWorkspace(t.Context(), ledger.Workspace{ID: scope, Name: string(scope)}, actor); err != nil {
		t.Fatal(err)
	}
	const plan, contracts = "plan-1", 50
	err = store.Write(t.Context(), scope, "reach.world", func(tx ports.WriteTx) error {
		if err := tx.Planning().InsertPlan(planning.Plan{ID: plan, Title: "Plan", Status: planning.StatusActive}); err != nil {
			return err
		}
		for i := range contracts {
			contract := planning.Contract{ID: fmt.Sprintf("contract-%d", i), Version: 1, PlanID: plan, Title: "Contract", Status: planning.StatusActive}
			if err := tx.Planning().InsertContract(contract); err != nil {
				return err
			}
			event, err := planning.RecordEvent(planning.EventContractVersion, planning.SubjectContract, contract.ID, contract, actor)
			if err != nil {
				return err
			}
			if _, err := tx.Append(event); err != nil {
				return err
			}
		}
		for i := range changes {
			change := workorders.WorkOrder{
				ID: fmt.Sprintf("change-%d", i), Kind: workorders.KindChange, State: workorders.Done, Title: "Change", RepositoryID: "repo-1",
				ContractID: fmt.Sprintf("contract-%d", i%contracts), ContractVersion: 1, MaxAttempts: 1,
			}
			review := workorders.WorkOrder{
				ID: fmt.Sprintf("review-%d", i), Kind: workorders.KindReview, State: workorders.Ready, Title: "Review", RepositoryID: "repo-1",
				ContractID: change.ContractID, ContractVersion: 1, Reviews: change.ID, MaxAttempts: 1,
			}
			for _, order := range []workorders.WorkOrder{change, review} {
				if err := tx.WorkOrders().Insert(order); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := questions.Decision{ID: "d-1", Status: questions.DecisionDecided, Scope: "plan:" + plan}
	errMeasured := errors.New("measured")
	fastest := time.Duration(0)
	err = store.Write(t.Context(), scope, "reach.cost", func(tx ports.WriteTx) error {
		for range 3 {
			start := time.Now()
			touched, err := reachOf(tx, decision)
			elapsed := time.Since(start)
			if err != nil {
				return err
			}
			if len(touched.orders) != changes || len(touched.told) != changes {
				return fmt.Errorf("the reach binds %d changes and tells %d reviews, want %d of each", len(touched.orders), len(touched.told), changes)
			}
			if fastest == 0 || elapsed < fastest {
				fastest = elapsed
			}
		}
		return errMeasured
	})
	if !errors.Is(err, errMeasured) {
		t.Fatal(err)
	}
	return fastest
}
