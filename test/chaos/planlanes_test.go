//go:build chaos

package chaos

import (
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
)

func roleReviewPlan(c *child) error {
	_, err := control.NewWorkOrders(c.store).ReviewPlan(c.ctx, c.scope, c.spec.WorkOrder, c.spec.Key, c.member())
	return err
}

// roleTakeInPlan reads one accepted plan. The source is new on every start, as a commit reads the
// same to a process that never saw it.
func roleTakeInPlan(c *child) error {
	source := fake.NewWorkSource()
	source.Write("contract.md", []byte(contract.ContractText(c.spec.Key, "Alpha", "One.")))
	open := func(context.Context, string, string) (ports.WorkSource, error) { return source, nil }
	_, err := control.NewIntake(c.store).TakeInPlans(c.ctx, c.scope, open)
	return err
}

// planIsIn proves a killed read finished on its retry: the plan's commit is on record once, with one
// version of the one contract it left, beside the contract the plan's own work pins.
func planIsIn(w *world, spec childSpec) error {
	events, err := w.rig.Events(w.store, w.rig.Scope)
	if err != nil {
		return err
	}
	versions, orders := 0, 0
	for _, e := range events {
		switch e.Type {
		case planning.EventContractVersion:
			versions++
		case workorders.EventCreated:
			orders++
		}
	}
	if versions != 2 || orders != 2 {
		return contract.Violatedf("%d versions and %d work orders, want the plan's own and one more of each", versions, orders)
	}
	if err := countEvents(w, workorders.EventPlanTakenIn, spec.WorkOrder, 1); err != nil {
		return err
	}
	row, err := w.rig.WorkOrder(w.store, spec.WorkOrder)
	if err != nil {
		return err
	}
	if row.PlanCommit == "" {
		return contract.Violatedf("the retry left %+v", row)
	}
	return nil
}

func takeInCase(op string, steps []string) crashCase {
	return crashCase{
		name: "take in an accepted plan: " + op, op: op, steps: steps,
		prepare: func(w *world) (childSpec, error) {
			id, err := w.rig.PlanLaneDone(w.store)
			return childSpec{Role: "take-in-plan", WorkOrder: id, Key: w.rig.ID("alpha")}, err
		},
		settled: planIsIn,
	}
}

func planLaneCases() []crashCase {
	return []crashCase{
		takeInCase("intake.plan", withSteps("polled", "recorded", "settled")),
		{
			name: "start a contract review", op: "work_order.review_plan", steps: withSteps("recorded"),
			prepare: func(w *world) (childSpec, error) {
				plan, _, err := w.rig.APlan(w.store)
				return childSpec{Role: "review-plan", WorkOrder: w.rig.ID("review"), Key: plan}, err
			},
			settled: func(w *world, spec childSpec) error {
				if err := countEvents(w, workorders.EventCreated, spec.WorkOrder, 1); err != nil {
					return err
				}
				if err := countEvents(w, workorders.EventReviewPassed, spec.WorkOrder, 1); err != nil {
					return err
				}
				return hasState(w, spec.WorkOrder, workorders.Ready)
			},
		},
	}
}

func planLaneVariants() []variant {
	return []variant{
		{name: "a review that passes a version it never read", control: control.Sabotage{PassesAnyVersion: true}, check: "PL9"},
		{name: "a review's questions climb while it reads", control: control.Sabotage{ReleasesReviews: true}, check: "PL6"},
		{name: "a plan read before anyone accepted it", control: control.Sabotage{TakesInUnaccepted: true}, check: "PL12"},
		{name: "an accepted plan that moves its source", control: control.Sabotage{MovesPlanCursor: true}, check: "PL15"},
		{
			name: "a blocking count that reads no plan", check: "PL5",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question.open_blocking", "OR (subject_kind = 'plan' AND subject_id = :plan_id)", "OR (subject_kind = 'plan' AND subject_id = :plan_id AND 1 = 0)")},
		},
	}
}

var _ = fmt.Sprint
