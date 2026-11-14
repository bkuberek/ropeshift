package delivery_test

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/workorders"
)

const hour = int64(60 * 60 * 1000)

// estimated is the keys of the work order being estimated: a medium change in repo-1, by the implement class,
// at person_approves, with 3 criteria.
var estimated = delivery.Keys{
	Kind: string(workorders.KindChange), Size: "medium", CapabilityClass: "implement", Repository: "repo-1", MergeAuthority: "person_approves", Criteria: 3,
}

// finished is a done delivery record with the estimated keys, changed by change, of a delivery time and a cost.
func finished(id string, ended, deliveryMs int64, cost float64, change func(*delivery.Keys)) delivery.Record {
	keys := estimated
	if change != nil {
		change(&keys)
	}
	return delivery.Record{WorkOrder: id, Keys: keys, Outcome: workorders.Done, Ended: ended, DeliveryMs: deliveryMs, Usage: delivery.Usage{CostUSD: cost}}
}

func otherRepository(k *delivery.Keys) { k.Repository = "repo-2" }
func manyCriteria(k *delivery.Keys)    { k.Criteria = 9 }

func estimateOf(records []delivery.Record, policy delivery.EstimatePolicy) delivery.Estimate {
	return delivery.EstimateOf(delivery.EstimateInput{WorkOrder: "new", Keys: estimated, Records: records, Policy: policy, PolicyVersion: 3, Rev: 77})
}

// TestTheFirstTierWithEnoughRecordsGivesAConfidentEstimate holds FR-EST-6: the narrowest tier holding at least
// min_samples of the most recent comparable records that ended done gives p50 and p85 by nearest rank, and the
// estimate names its tier, keys, samples, and the work orders it drew on.
func TestTheFirstTierWithEnoughRecordsGivesAConfidentEstimate(t *testing.T) {
	var records []delivery.Record
	for i := range 10 {
		// Ten that match only from tier 2, since they have more criteria.
		records = append(records, finished(fmt.Sprintf("t2-%02d", i), int64(100+i), int64(i+1)*hour, float64(i+1), manyCriteria))
	}
	records = append(records, finished("same", 50, 30*hour, 30, nil))
	records = append(records, delivery.Record{WorkOrder: "failed", Keys: estimated, Outcome: workorders.Failed, Ended: 200, DeliveryMs: hour})
	e := estimateOf(records, delivery.DefaultEstimatePolicy())
	if e.Status != delivery.Estimated || e.Confidence != delivery.Confident || e.Tier != 2 || e.SampleCount != 11 || e.MinSamples != 10 {
		t.Fatalf("estimate %+v", e)
	}
	// Eleven delivery times 1h to 10h and 30h: nearest rank p50 is the 6th, 6h; p85 the 10th, 10h.
	if e.Time() != (delivery.Band{P50: 6 * hour, P85: 10 * hour}) || e.Cost() != (delivery.CostBand{P50: 6, P85: 10}) || e.P85Open {
		t.Errorf("bands %+v %+v", e.Time(), e.Cost())
	}
	if !reflect.DeepEqual(e.DroppedKeys, []string{delivery.KeyCriteria}) || len(e.Keys) != 5 || len(e.SampleIDs) != 11 || e.SampleIDs[0] != "t2-09" {
		t.Errorf("basis keys %v dropped %v drawn %v", e.Keys, e.DroppedKeys, e.SampleIDs)
	}
	if e.DeliveryMinMs != hour || e.DeliveryMaxMs != 30*hour || e.CostMinUSD != 1 || e.CostMaxUSD != 30 || e.Rev != 77 || e.PolicyVersion != 3 || e.EstimatorVersion != delivery.EstimatorVersion {
		t.Errorf("trace %+v", e)
	}
	if want := "Based on 11 finished medium changes with the same capability class, repository, and merge authority level."; e.Explanation != want {
		t.Errorf("explanation %q", e.Explanation)
	}
}

// TestOnlyTheMostRecentComparableRecordsCount reads at most recent_samples of a tier, newest by end first.
func TestOnlyTheMostRecentComparableRecordsCount(t *testing.T) {
	var records []delivery.Record
	for i := range 60 {
		records = append(records, finished(fmt.Sprintf("r-%02d", i), int64(i), hour, 1, nil))
	}
	e := estimateOf(records, delivery.DefaultEstimatePolicy())
	if e.Tier != 1 || e.SampleCount != 50 || e.SampleIDs[0] != "r-59" || e.SampleIDs[49] != "r-10" {
		t.Errorf("estimate drew %d from tier %d: %v", e.SampleCount, e.Tier, e.SampleIDs)
	}
}

// TestBelowTheMinimumTheEstimateIsLowConfidenceWithItsReasons holds FR-EST-7 and FR-EST-14 with ADR-0026's own
// example: the tier with the most records, ranks by (n + 1), the band open above the longest below 6 records,
// and the explanation in plain words.
func TestBelowTheMinimumTheEstimateIsLowConfidenceWithItsReasons(t *testing.T) {
	records := []delivery.Record{
		finished("a", 1, 2*hour, 1, otherRepository),
		finished("b", 2, 5*hour, 2, otherRepository),
		finished("c", 3, 7*hour, 3, func(k *delivery.Keys) { k.Repository, k.Criteria = "repo-3", 8 }),
		finished("d", 4, 11*hour, 4, otherRepository),
	}
	e := estimateOf(records, delivery.DefaultEstimatePolicy())
	if e.Status != delivery.Estimated || e.Confidence != delivery.Low || e.Tier != 3 || e.SampleCount != 4 {
		t.Fatalf("estimate %+v", e)
	}
	// n = 4: p50 at rank ceil(2.5) = 3, 7h; p85 rank ceil(4.25) = 5 is past n, so open above the longest.
	if e.Time() != (delivery.Band{P50: 7 * hour, P85: 11 * hour}) || !e.P85Open || e.Cost() != (delivery.CostBand{P50: 3, P85: 4}) {
		t.Errorf("bands %+v %+v open %v", e.Time(), e.Cost(), e.P85Open)
	}
	want := "Low confidence. Based on 4 finished medium changes; a confident estimate needs 10. To find them, ropeshift ignored the repository and the number of criteria. " +
		"They took from 2h to 11h. With fewer than 6, the 85th percentile cannot be read from them, so the band is open above 11h."
	if e.Explanation != want {
		t.Errorf("explanation\n got %q\nwant %q", e.Explanation, want)
	}
}

// TestALowEstimateGoesBroaderOnlyWhenThatAddsRecords keeps the narrowest tier among those with the most records.
func TestALowEstimateGoesBroaderOnlyWhenThatAddsRecords(t *testing.T) {
	records := []delivery.Record{finished("a", 1, 4*hour, 1, nil), finished("b", 2, 6*hour, 1, nil)}
	e := estimateOf(records, delivery.DefaultEstimatePolicy())
	if e.Confidence != delivery.Low || e.Tier != 1 || len(e.DroppedKeys) != 0 {
		t.Errorf("two records of tier 1 estimated from tier %d dropping %v", e.Tier, e.DroppedKeys)
	}
	one := estimateOf([]delivery.Record{finished("a", 1, 4*hour, 1.5, nil)}, delivery.DefaultEstimatePolicy())
	if one.Time() != (delivery.Band{P50: 4 * hour, P85: 4 * hour}) || !one.P85Open {
		t.Errorf("one record reads %+v open %v", one.Time(), one.P85Open)
	}
	if want := "Low confidence. Based on 1 finished medium change; a confident estimate needs 10. It took 4h. With fewer than 6, the 85th percentile cannot be read from them, so the band is open above 4h."; one.Explanation != want {
		t.Errorf("explanation %q", one.Explanation)
	}
}

// TestWithNoComparableWorkThereIsNoNumber holds FR-EST-14's absence words, for a project that shares its history
// and one that does not, and tier 6 from the installation pool when it shares.
func TestWithNoComparableWorkThereIsNoNumber(t *testing.T) {
	other := finished("x", 1, hour, 1, func(k *delivery.Keys) { k.Size = "large" })
	e := estimateOf([]delivery.Record{other}, delivery.DefaultEstimatePolicy())
	if e.Status != delivery.NoHistory || e.Confidence != "" || e.SampleCount != 0 || e.DeliveryP50Ms != 0 {
		t.Fatalf("estimate %+v", e)
	}
	if want := "No estimate: no medium change has finished in this project yet, and this project does not share history with other projects."; e.Explanation != want {
		t.Errorf("explanation %q", e.Explanation)
	}
	sharing := delivery.DefaultEstimatePolicy()
	sharing.ShareHistory = true
	e = estimateOf(nil, sharing)
	if want := "No estimate: no medium change has finished in this project yet or in the installation pool."; e.Explanation != want {
		t.Errorf("explanation %q", e.Explanation)
	}
	pool := []delivery.Record{{Keys: delivery.Keys{Kind: estimated.Kind, Size: estimated.Size}, Outcome: workorders.Done, Ended: 1, DeliveryMs: 3 * hour, Usage: delivery.Usage{CostUSD: 2}}}
	e = delivery.EstimateOf(delivery.EstimateInput{Keys: estimated, Pool: pool, Policy: sharing})
	if e.Confidence != delivery.Low || e.Tier != 6 || e.SampleCount != 1 || len(e.SampleIDs) != 0 {
		t.Errorf("a pooled estimate %+v", e)
	}
	if e = delivery.EstimateOf(delivery.EstimateInput{Keys: estimated, Pool: pool, Policy: delivery.DefaultEstimatePolicy()}); e.Status != delivery.NoHistory {
		t.Errorf("a project that does not share drew on the pool: %+v", e)
	}
}

// TestACostBandHoldingALowerBoundReadsAtLeast marks a band one of whose records reported only part of its cost.
func TestACostBandHoldingALowerBoundReadsAtLeast(t *testing.T) {
	bounded := finished("b", 2, hour, 1, nil)
	bounded.Usage.CostLowerBound = true
	if e := estimateOf([]delivery.Record{finished("a", 1, hour, 1, nil), bounded}, delivery.DefaultEstimatePolicy()); !e.CostAtLeast {
		t.Errorf("a band with a lower bound %+v", e)
	}
}

// TestTheEstimatePolicyHoldsItsFloor never lets a confident estimate rest on fewer than 5 records.
func TestTheEstimatePolicyHoldsItsFloor(t *testing.T) {
	p := delivery.DefaultEstimatePolicy()
	if p.MinSamples != 10 || p.RecentSamples != 50 || p.ForecastTrials != 2000 || p.ShareHistory || p.Check() != nil {
		t.Errorf("defaults %+v: %v", p, p.Check())
	}
	for name, bad := range map[string]delivery.EstimatePolicy{
		"below the floor":         {MinSamples: 4, RecentSamples: 50, ForecastTrials: 2000},
		"fewer recent than needs": {MinSamples: 10, RecentSamples: 9, ForecastTrials: 2000},
		"no trials":               {MinSamples: 10, RecentSamples: 50},
	} {
		if bad.Check() == nil {
			t.Errorf("%s: %+v passed", name, bad)
		}
	}
}

// TestEveryEstimateTracesToTheRecordsItNames holds the estimator's promise on generated histories: each number
// of a band is the delivery time or cost of a record the estimate drew on, the confidence follows the minimum
// sample, and a band is open only below 6 records of a low-confidence estimate.
func TestEveryEstimateTracesToTheRecordsItNames(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261114, 6))
	sizes, classes, repositories := []string{"small", "medium"}, []string{"implement", "review"}, []string{"repo-1", "repo-2"}
	for run := range 2000 {
		var records []delivery.Record
		for i := range rng.IntN(40) {
			outcome := workorders.Done
			if rng.IntN(5) == 0 {
				outcome = workorders.Failed
			}
			records = append(records, delivery.Record{
				WorkOrder: fmt.Sprintf("wo-%d", i), Outcome: outcome, Ended: int64(rng.IntN(1000)), DeliveryMs: int64(1 + rng.IntN(100)), Usage: delivery.Usage{CostUSD: float64(rng.IntN(50))},
				Keys: delivery.Keys{
					Kind: string(workorders.KindChange), Size: sizes[rng.IntN(2)], CapabilityClass: classes[rng.IntN(2)], Repository: repositories[rng.IntN(2)],
					MergeAuthority: "person_approves", Criteria: int64(1 + rng.IntN(9)),
				},
			})
		}
		policy := delivery.EstimatePolicy{MinSamples: int64(5 + rng.IntN(6)), RecentSamples: 50, ForecastTrials: 1}
		e := delivery.EstimateOf(delivery.EstimateInput{Keys: estimated, Records: records, Policy: policy})
		byID := map[string]delivery.Record{}
		for _, r := range records {
			byID[r.WorkOrder] = r
		}
		if e.Status == delivery.NoHistory {
			if e.SampleCount != 0 || e.Confidence != "" {
				t.Fatalf("run %d: no history with %+v", run, e)
			}
			continue
		}
		if e.SampleCount != len(e.SampleIDs) || (e.Confidence == delivery.Confident) != (int64(e.SampleCount) >= policy.MinSamples) || (e.P85Open && (e.Confidence != delivery.Low || e.SampleCount >= 6)) {
			t.Fatalf("run %d: estimate %+v", run, e)
		}
		times, costs := map[int64]bool{}, map[float64]bool{}
		for _, id := range e.SampleIDs {
			r, ok := byID[id]
			if !ok || r.Outcome != workorders.Done || r.Size != estimated.Size {
				t.Fatalf("run %d: drew on %q, which is no finished comparable record", run, id)
			}
			times[r.DeliveryMs], costs[r.Usage.CostUSD] = true, true
		}
		if !times[e.DeliveryP50Ms] || !times[e.DeliveryP85Ms] || !costs[e.CostP50USD] || !costs[e.CostP85USD] || e.DeliveryP50Ms > e.DeliveryP85Ms || e.Explanation == "" {
			t.Fatalf("run %d: a band %+v %+v not drawn from its records", run, e.Time(), e.Cost())
		}
	}
}

// TestAnyPolicyGivesASafeEstimate holds the estimator to its floor whatever policy it is handed: an empty
// policy reads as the default, no recent window reads as the default window, and a window narrower than the
// minimum widens to it, so no estimate panics, hides history, or rests on fewer records than it has.
func TestAnyPolicyGivesASafeEstimate(t *testing.T) {
	var eight []delivery.Record
	for i := range 8 {
		eight = append(eight, finished(fmt.Sprintf("r-%d", i), int64(i), int64(i+1)*hour, float64(i+1), nil))
	}
	if e := estimateOf(eight, delivery.EstimatePolicy{}); e.Status != delivery.Estimated || e.Confidence != delivery.Low || e.SampleCount != 8 || e.MinSamples != 10 {
		t.Errorf("an empty policy gave %+v", e)
	}
	if e := estimateOf(eight, delivery.EstimatePolicy{MinSamples: 10}); e.Status != delivery.Estimated || e.SampleCount != 8 {
		t.Errorf("no recent window gave %+v", e)
	}
	if e := estimateOf(eight, delivery.EstimatePolicy{MinSamples: 10, RecentSamples: 3}); e.SampleCount != 8 {
		t.Errorf("a window of 3 under a minimum of 10 drew %d of 8", e.SampleCount)
	}
	if e := estimateOf(eight, delivery.EstimatePolicy{MinSamples: 2, RecentSamples: 50}); e.Confidence != delivery.Confident || e.MinSamples != delivery.MinSamplesFloor {
		t.Errorf("a minimum below the floor gave %+v", e)
	}
}

// TestASizeLeftOutReadsAsUnknown matches a work order that declares no size with the records of unknown size, as
// delivery records keep them.
func TestASizeLeftOutReadsAsUnknown(t *testing.T) {
	unknown := finished("a", 1, hour, 1, func(k *delivery.Keys) { k.Size = "unknown" })
	keys := estimated
	keys.Size = ""
	e := delivery.EstimateOf(delivery.EstimateInput{Keys: keys, Records: []delivery.Record{unknown}, Policy: delivery.DefaultEstimatePolicy()})
	if e.Status != delivery.Estimated || e.SampleCount != 1 {
		t.Errorf("a work order of no size estimated %+v", e)
	}
}

// pooled is a record of another project in the installation's pool, which names no work order.
func pooled(ended, deliveryMs int64, cost float64) delivery.Record {
	return delivery.Record{Keys: delivery.Keys{Kind: estimated.Kind, Size: estimated.Size, CapabilityClass: "implement"}, Outcome: workorders.Done, Ended: ended, DeliveryMs: deliveryMs, Usage: delivery.Usage{CostUSD: cost}}
}

// TestRecordsThatEndTogetherDrawTheSameWhateverTheirOrder breaks ties in recency by the records themselves, so
// pool records, which name no work order, give one estimate whatever order they come in.
func TestRecordsThatEndTogetherDrawTheSameWhateverTheirOrder(t *testing.T) {
	sharing := delivery.DefaultEstimatePolicy()
	sharing.ShareHistory = true
	var forward, reversed []delivery.Record
	for i := range 60 {
		forward = append(forward, pooled(5, int64(i+1), float64(60-i)))
	}
	for i := len(forward) - 1; i >= 0; i-- {
		reversed = append(reversed, forward[i])
	}
	a := delivery.EstimateOf(delivery.EstimateInput{Keys: estimated, Pool: forward, Policy: sharing})
	b := delivery.EstimateOf(delivery.EstimateInput{Keys: estimated, Pool: reversed, Policy: sharing})
	if !reflect.DeepEqual(a, b) || a.SampleCount != 50 {
		t.Errorf("the order of the pool changed the estimate:\n%+v\n%+v", a, b)
	}
}

// TestAPooledEstimateNamesTheProjectsOwnWork holds FR-EST-6 at tier 6: the pool holds other projects' records,
// which name nothing, so a pooled estimate names the project's own work orders it drew on and counts the rest.
func TestAPooledEstimateNamesTheProjectsOwnWork(t *testing.T) {
	sharing := delivery.DefaultEstimatePolicy()
	sharing.ShareHistory = true
	own := []delivery.Record{
		finished("own-1", 1, 2*hour, 1, func(k *delivery.Keys) { k.CapabilityClass, k.MergeAuthority = "review", "person_merges" }),
		finished("own-2", 2, 3*hour, 1, func(k *delivery.Keys) { k.CapabilityClass, k.MergeAuthority = "review", "person_merges" }),
	}
	pool := []delivery.Record{pooled(3, 4*hour, 2), pooled(4, 5*hour, 2), pooled(5, 6*hour, 2)}
	e := delivery.EstimateOf(delivery.EstimateInput{Keys: estimated, Records: own, Pool: pool, Policy: sharing})
	if e.Tier != 6 || e.SampleCount != 5 || e.PoolSamples != 3 || !reflect.DeepEqual(e.SampleIDs, []string{"own-2", "own-1"}) {
		t.Errorf("a pooled estimate drew %d, %d from the pool, naming %v, at tier %d", e.SampleCount, e.PoolSamples, e.SampleIDs, e.Tier)
	}
}
