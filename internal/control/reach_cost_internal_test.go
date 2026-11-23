package control

import (
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// reachGrowthBound is how many times as much computing one decision's reach may cost on the larger ledger of the test
// below, which holds sixteen times the work orders of the smaller. A reach that reads each work order once costs about
// sixteen times as much, or less where part of its cost does not grow with the ledger, and one that reads the ledger again
// for each work order costs the square of that, about two hundred and fifty-six times as much. The bound of sixty-four
// stands between them with room for what a loaded machine adds to either measure, which a bound on the time itself has
// no room for.
const reachGrowthBound = 64

// TestADecisionsReachStaysCheapOnALargeLedger measures what computing a decision's reach costs: a plan scope over finished
// changes under 50 contracts, each change with a review that waits, so the reach reads every work order, resolves the plan
// of each contract version once, and finds the review of each change it binds. It measures two sizes and holds how the
// cost grows to a bound, so it fails on a change in how the cost grows and not on a slow machine. The two sizes are
// measured in turn, so a burst of load slows both, and only the fastest computation of each counts. It measures at least
// five rounds, and goes on up to fifteen, for at most a minute, until the larger is within the bound.
func TestADecisionsReachStaysCheapOnALargeLedger(t *testing.T) {
	const fewer, more, rounds = 250, 4000, 5
	measureFewer, measureMore := reachMeasure(t, fewer), reachMeasure(t, more)
	smallest, largest := time.Duration(1<<63-1), time.Duration(1<<63-1)
	ends := time.Now().Add(time.Minute)
	for round := 1; round <= 3*rounds && time.Now().Before(ends); round++ {
		smallest, largest = min(smallest, measureFewer()), min(largest, measureMore())
		if round >= rounds && largest < reachGrowthBound*smallest {
			break
		}
	}
	t.Logf("reach of a plan decision: %v over %d work orders, %v over %d", smallest, 2*fewer, largest, 2*more)
	if largest >= reachGrowthBound*smallest {
		t.Errorf("the reach over %d work orders took at best %v, and over %d took at best %v: that is %.1f times as much, over the bound of %d",
			2*fewer, smallest, 2*more, largest, float64(largest)/float64(smallest), reachGrowthBound)
	}
}

// reachMeasure puts changes, each with its review, under one plan, and returns what computes the reach of a decision on
// that plan once and says how long it took. It writes the rows the reach reads directly, with an event for each contract
// version and none for each work order, which the reach never reads.
func reachMeasure(t *testing.T, changes int) func() time.Duration {
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
	return func() time.Duration {
		var elapsed time.Duration
		err := store.Write(t.Context(), scope, "reach.cost", func(tx ports.WriteTx) error {
			runtime.GC()
			start := time.Now()
			touched, err := reachOf(tx, decision)
			elapsed = time.Since(start)
			if err != nil {
				return err
			}
			if len(touched.orders) != changes || len(touched.told) != changes {
				return fmt.Errorf("the reach binds %d changes and tells %d reviews, want %d of each", len(touched.orders), len(touched.told), changes)
			}
			return errMeasured
		})
		if !errors.Is(err, errMeasured) {
			t.Fatal(err)
		}
		return elapsed
	}
}
