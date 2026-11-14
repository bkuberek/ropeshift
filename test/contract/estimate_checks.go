package contract

import (
	"errors"
	"reflect"
	"slices"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// EstimateChecks hold estimate sets (ADR-0026, FR-EST-10, FR-EST-14): each kept whole with what it showed, and
// each work order's newest estimate its current one.
var EstimateChecks = []Check{
	{"ES1", "a set keeps its estimates whole, and a later set of a work order holds its current estimate while the earlier set keeps its own", checkEstimateSets},
	{"ES2", "work made outside a plan is estimated when it is made, from the finished work the fold kept, and a retry estimates nothing again", checkEstimateAtCreation},
	{"ES3", "intake estimates the open work of each plan version it records in one set, apart from the plan's digest", checkEstimateAtIntake},
}

// addSet keeps a sealed set in a transaction of its own, which appends a note, as every write must append an event.
func (r *Rig) addSet(s ports.Store, set delivery.EstimateSet) error {
	return s.Write(r.Ctx, r.Scope, "estimate.add", func(tx ports.WriteTx) error {
		if _, err := tx.Append(r.noteDraft(r.ID("note"))); err != nil {
			return err
		}
		return tx.Estimates().Add(set)
	})
}

// estimates reads one set and the current estimate of each work order named.
func (r *Rig) estimates(s ports.Store, set string, orders ...string) (delivery.EstimateSet, map[string]delivery.Estimate, error) {
	var read delivery.EstimateSet
	current := map[string]delivery.Estimate{}
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		if read, err = tx.Estimates().Set(set); err != nil {
			return err
		}
		for _, order := range orders {
			e, err := tx.Estimates().Current(order)
			if errors.Is(err, ports.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			current[order] = e
		}
		return nil
	})
	return read, current, err
}

func checkEstimateSets(r *Rig) error {
	s := r.Store()
	a, b := r.ID("wo-a"), r.ID("wo-b")
	first := delivery.EstimateSet{
		ID: r.ID("set-1"), Plan: r.ID("plan"), PlanVersion: 2, Rev: 7, EstimatorVersion: delivery.EstimatorVersion, PolicyVersion: 1, CreatedAt: 1000,
		Estimates: []delivery.Estimate{
			{ID: r.ID("e-1"), WorkOrder: a, Status: delivery.Estimated, Confidence: delivery.Low, Tier: 3, Keys: []string{"kind", "size"}, DroppedKeys: []string{"criteria"},
				SampleCount: 2, MinSamples: 10, SampleIDs: []string{"done-2", "done-1"}, DeliveryP50Ms: 3600000, DeliveryP85Ms: 7200000, DeliveryMinMs: 3600000,
				DeliveryMaxMs: 7200000, CostP50USD: 1.5, CostP85USD: 2, CostMinUSD: 1.5, CostMaxUSD: 2, P85Open: true, CostAtLeast: true, Rev: 7,
				EstimatorVersion: delivery.EstimatorVersion, PolicyVersion: 1, Explanation: "Only 2 finished medium changes."},
			{ID: r.ID("e-2"), WorkOrder: b, Status: delivery.NoHistory, Keys: []string{}, DroppedKeys: []string{}, SampleIDs: []string{}, MinSamples: 10, Rev: 7,
				EstimatorVersion: delivery.EstimatorVersion, PolicyVersion: 1, Explanation: "No estimate: no large change has finished in this project yet."},
		},
	}.Seal()
	if err := r.addSet(s, first); err != nil {
		return err
	}
	read, current, err := r.estimates(s, first.ID, a, b)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(read, first) {
		return Violatedf("the set read %+v, kept %+v", read, first)
	}
	if !reflect.DeepEqual(current[a], first.Estimates[0]) || !reflect.DeepEqual(current[b], first.Estimates[1]) {
		return Violatedf("the current estimates %+v", current)
	}
	later := delivery.EstimateSet{ID: r.ID("set-2"), Rev: 9, EstimatorVersion: delivery.EstimatorVersion, PolicyVersion: 1, CreatedAt: 2000, Estimates: []delivery.Estimate{
		{ID: r.ID("e-3"), WorkOrder: a, Status: delivery.Estimated, Confidence: delivery.Confident, Tier: 1, Keys: []string{"kind", "size"}, DroppedKeys: []string{},
			SampleCount: 10, MinSamples: 10, SampleIDs: []string{"done-3"}, Rev: 9, EstimatorVersion: delivery.EstimatorVersion, PolicyVersion: 1, Explanation: "Based on 10 finished medium changes."},
	}}.Seal()
	if err := r.addSet(s, later); err != nil {
		return err
	}
	read, current, err = r.estimates(s, first.ID, a, b)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current[a], later.Estimates[0]) || !reflect.DeepEqual(current[b], first.Estimates[1]) {
		return Violatedf("after a later set, the current estimates %+v", current)
	}
	if read.Digest != first.Digest || read.Estimates[0].Current || !read.Estimates[1].Current || read.Estimates[0].Explanation != first.Estimates[0].Explanation {
		return Violatedf("after a later set, the first set reads %+v", read)
	}
	if _, _, err := r.estimates(s, r.ID("no-set")); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("an unknown set read: %v", err)
	}
	return nil
}

// currentOf reads a work order's current estimate and the set that holds it.
func (r *Rig) currentOf(s ports.Store, order string) (delivery.Estimate, delivery.EstimateSet, error) {
	var e delivery.Estimate
	var set delivery.EstimateSet
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		if e, err = tx.Estimates().Current(order); err != nil {
			return err
		}
		set, err = tx.Estimates().Set(e.SetID)
		return err
	})
	return e, set, err
}

// madeNow says an estimate was made by this build under the built-in policy, at a revision, with its words.
func madeNow(e delivery.Estimate, set delivery.EstimateSet) bool {
	return e.ID != "" && e.Current && e.Rev > 0 && e.Rev == set.Rev && e.EstimatorVersion == delivery.EstimatorVersion &&
		e.PolicyVersion == 1 && set.PolicyVersion == 1 && e.Explanation != "" && e.MinSamples == 10 && set.Digest != ""
}

func checkEstimateAtCreation(r *Rig) error {
	s := r.Store()
	first, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(r.ID("commit"), LaneCheck(criterionOne, true)))
	if err != nil {
		return err
	}
	made, set, err := r.currentOf(s, first)
	if err != nil {
		return err
	}
	if made.Status != delivery.NoHistory || set.Plan != "" || len(set.Estimates) != 1 || !madeNow(made, set) {
		return Violatedf("the first work, with no history, is estimated %+v in %+v", made, set)
	}
	if _, err := r.NewOps(s).Accept(r.Ctx, r.Scope, first, r.Actor); err != nil {
		return err
	}
	if _, err := control.NewDeliveryFold(s).Fold(r.Ctx, r.Scope); err != nil {
		return err
	}
	reviews, err := r.reviewsOf(s, first)
	if err != nil {
		return err
	}
	for _, review := range reviews {
		if _, _, err := r.currentOf(s, review.ID); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("a review is estimated: %v", err)
		}
	}
	work := control.LedgerWork{
		Order:     workorders.WorkOrder{ID: r.ID("wo-next"), Kind: workorders.KindChange, Title: "Do it again", RepositoryID: AcceptanceRepository, MaxAttempts: 1},
		Statement: "Do the work again.",
		Criteria:  []planning.Criterion{{ID: criterionOne, Text: "It holds: " + criterionOne}},
	}
	next, err := r.NewOps(s).Record(r.Ctx, r.Scope, work, r.Actor)
	if err != nil {
		return err
	}
	made, set, err = r.currentOf(s, next.ID)
	if err != nil {
		return err
	}
	if made.Status != delivery.Estimated || made.Confidence != delivery.Low || made.Tier != 1 || !slices.Equal(made.SampleIDs, []string{first}) || !madeNow(made, set) {
		return Violatedf("work like the finished one is estimated %+v in %+v", made, set)
	}
	if _, err := r.NewOps(s).Record(r.Ctx, r.Scope, work, r.Actor); err != nil {
		return err
	}
	again, _, err := r.currentOf(s, next.ID)
	if err != nil || again.ID != made.ID {
		return Violatedf("a retry estimated it again as %+v: %v", again, err)
	}
	created, err := r.NewWorkOrder(s)
	if err != nil {
		return err
	}
	if made, set, err = r.currentOf(s, created.ID); err != nil || made.Status != delivery.NoHistory || !madeNow(made, set) {
		return Violatedf("a medium change, which has no history, is estimated %+v: %v", made, err)
	}
	return nil
}

// ordersOfContract lists the work orders of a contract, by ID.
func (r *Rig) ordersOfContract(s ports.Store, contract string) ([]string, error) {
	var out []string
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().ByContract(contract)
		for _, row := range rows {
			out = append(out, row.ID)
		}
		return err
	})
	slices.Sort(out)
	return out, err
}

func checkEstimateAtIntake(r *Rig) error {
	s := r.Store()
	files := newApprovalSource()
	files.source.Write("solo.md", []byte(placedContract{ID: "solo"}.text()))
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	versions, err := r.planVersionsOf(s, plan)
	if err != nil || len(versions) != 1 {
		return Violatedf("versions %+v: %v", versions, err)
	}
	budget, held, err := r.contractTitled(s, "budget")
	if err != nil || !held {
		return Violatedf("the contract: %v", err)
	}
	orders, err := r.ordersOfContract(s, budget.ID)
	if err != nil || len(orders) != 1 {
		return Violatedf("the plan's work %v: %v", orders, err)
	}
	made, set, err := r.currentOf(s, orders[0])
	if err != nil {
		return err
	}
	if set.Plan != plan || set.PlanVersion != 1 || len(set.Estimates) != 1 || made.Status != delivery.NoHistory || !madeNow(made, set) {
		return Violatedf("version 1's work is estimated %+v in %+v", made, set)
	}
	if versions[0].Digest != planning.PlanDigest(versions[0].Records) {
		return Violatedf("the estimates moved the plan's digest")
	}
	solo, held, err := r.contractTitled(s, "solo")
	if err != nil || !held {
		return Violatedf("the contract with no plan: %v", err)
	}
	alone, err := r.ordersOfContract(s, solo.ID)
	if err != nil || len(alone) != 1 {
		return Violatedf("the work of a contract with no plan %v: %v", alone, err)
	}
	if lone, loneSet, err := r.currentOf(s, alone[0]); err != nil || loneSet.Plan != "" || !madeNow(lone, loneSet) {
		return Violatedf("work with no plan is estimated %+v in %+v: %v", lone, loneSet, err)
	}

	// A poll that changes nothing estimates nothing again.
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	if again, _, err := r.currentOf(s, orders[0]); err != nil || again.ID != made.ID {
		return Violatedf("an unchanged poll estimated %+v: %v", again, err)
	}

	// A new version estimates all of its open work in a new set, and the first set keeps what it showed.
	files.source.Write("budget.md", []byte(placedContract{ID: "budget", Plan: approvalPlan, Orders: []orderIn{{ID: "budget-order"}, {ID: "budget-more", Size: "large"}}}.text()))
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	if orders, err = r.ordersOfContract(s, budget.ID); err != nil || len(orders) != 2 {
		return Violatedf("the plan's work after a change %v: %v", orders, err)
	}
	latest, latestSet, err := r.currentOf(s, orders[0])
	if err != nil {
		return err
	}
	if latestSet.Plan != plan || latestSet.PlanVersion != 2 || len(latestSet.Estimates) != 2 || latestSet.ID == set.ID || !madeNow(latest, latestSet) {
		return Violatedf("version 2's work is estimated %+v in %+v", latest, latestSet)
	}
	kept, _, err := r.estimates(s, set.ID)
	if err != nil || kept.Digest != set.Digest || len(kept.Estimates) != 1 || kept.Estimates[0].Current {
		return Violatedf("version 1's set now reads %+v: %v", kept, err)
	}
	return nil
}
