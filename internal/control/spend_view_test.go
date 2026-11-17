package control

import (
	"fmt"
	"math"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// fakeEstimates is what a work order's estimates are, and nothing else a store holds.
type fakeEstimates struct {
	ports.EstimateReader
	baseline map[string]delivery.Estimate
	current  map[string]delivery.Estimate
}

func (e fakeEstimates) BaselineOf(id string) (delivery.Baseline, delivery.Estimate, error) {
	if estimate, ok := e.baseline[id]; ok {
		return delivery.Baseline{ID: "b"}, estimate, nil
	}
	return delivery.Baseline{}, delivery.Estimate{}, fmt.Errorf("no baseline: %w", ports.ErrNotFound)
}

func (e fakeEstimates) Current(id string) (delivery.Estimate, error) {
	if estimate, ok := e.current[id]; ok {
		return estimate, nil
	}
	return delivery.Estimate{}, fmt.Errorf("no current estimate: %w", ports.ErrNotFound)
}

type orderReads struct {
	ports.WorkOrderReader
	orders map[string]workorders.WorkOrder
}

func (o orderReads) Get(id string) (workorders.WorkOrder, error) {
	if order, ok := o.orders[id]; ok {
		return order, nil
	}
	return workorders.WorkOrder{}, fmt.Errorf("no work order: %w", ports.ErrNotFound)
}

func band(outcomes int, p50, p85 float64) delivery.Estimate {
	return delivery.Estimate{Status: delivery.Estimated, SampleCount: outcomes, CostP50USD: p50, CostP85USD: p85}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// A work order's limits read its baselined band, and its current band only when it has no baseline. A baseline that
// holds no estimate leaves it to the size's limit, because the band recorded at approval is the one a limit reads, so
// it does not move as later estimates come.
func TestLimitsReadTheBaselineBeforeTheCurrentBand(t *testing.T) {
	policy := authority.DefaultSpendPolicy()
	change := func(id string) workorders.WorkOrder {
		return workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Size: "medium"}
	}
	tests := []struct {
		name       string
		baseline   map[string]delivery.Estimate
		current    map[string]delivery.Estimate
		soft, hard float64
		basis      delivery.LimitBasis
	}{
		{"the baselined band", map[string]delivery.Estimate{"w": band(8, 4, 10)}, map[string]delivery.Estimate{"w": band(8, 9, 30)}, 10, 20, delivery.BasisBaselinedBand},
		{"the current band with no baseline", nil, map[string]delivery.Estimate{"w": band(8, 4, 10)}, 10, 20, delivery.BasisCurrentBand},
		{"a baseline with no estimate is not replaced by a current band", map[string]delivery.Estimate{"w": {Status: delivery.NoHistory}}, map[string]delivery.Estimate{"w": band(8, 4, 10)}, 12.5, 25, delivery.BasisNoHistory},
		{"a band of five outcomes", map[string]delivery.Estimate{"w": band(5, 4, 10)}, nil, 12.5, 25, delivery.BasisNoHistory},
		{"no estimate at all", nil, nil, 12.5, 25, delivery.BasisNoHistory},
		{"a band open above its p50", map[string]delivery.Estimate{"w": {Status: delivery.Estimated, SampleCount: 9, CostP50USD: 4, CostP85USD: 4, P85Open: true}}, nil, 12.5, 25, delivery.BasisNoHistory},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reads := spendReads{
				orders:    orderReads{orders: map[string]workorders.WorkOrder{"w": change("w")}},
				estimates: fakeEstimates{baseline: tt.baseline, current: tt.current},
			}
			got, err := reads.limitsOf(policy, change("w"))
			if err != nil {
				t.Fatal(err)
			}
			if !near(got.Soft, tt.soft) || !near(got.Hard, tt.hard) || got.Basis != tt.basis {
				t.Errorf("limits %+v, want soft %v hard %v by %s", got, tt.soft, tt.hard, tt.basis)
			}
		})
	}
}

// A review's limits are a share of the limits of the work order it judges, and a review of work that is gone has none.
func TestAReviewsLimitsFollowTheWorkItJudges(t *testing.T) {
	policy := authority.DefaultSpendPolicy()
	reviewed := workorders.WorkOrder{ID: "w", Kind: workorders.KindChange, Size: "large"}
	review := workorders.WorkOrder{ID: "r", Kind: workorders.KindReview, Reviews: "w"}
	reads := spendReads{
		orders:    orderReads{orders: map[string]workorders.WorkOrder{"w": reviewed, "r": review}},
		estimates: fakeEstimates{baseline: map[string]delivery.Estimate{"w": band(8, 4, 10)}},
	}
	got, err := reads.limitsOf(policy, review)
	if err != nil || !near(got.Hard, 10) || !near(got.Soft, 5) || got.Basis != delivery.BasisReview {
		t.Errorf("a review of work limited at 20 has %+v, %v", got, err)
	}
	orphan := workorders.WorkOrder{ID: "o", Kind: workorders.KindReview, Reviews: "gone"}
	if got, err := reads.limitsOf(policy, orphan); err != nil || got.Limited() {
		t.Errorf("a review of work that is gone has %+v, %v", got, err)
	}
}
