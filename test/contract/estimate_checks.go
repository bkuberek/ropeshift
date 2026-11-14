package contract

import (
	"errors"
	"reflect"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
)

// EstimateChecks hold estimate sets (ADR-0026, FR-EST-10, FR-EST-14): each kept whole with what it showed, and
// each work order's newest estimate its current one.
var EstimateChecks = []Check{
	{"ES1", "a set keeps its estimates whole, and a later set of a work order holds its current estimate while the earlier set keeps its own", checkEstimateSets},
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
