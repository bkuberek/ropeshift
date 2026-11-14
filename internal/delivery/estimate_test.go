package delivery_test

import (
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
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
	policy.Version = 3
	return delivery.EstimateOf(delivery.EstimateInput{WorkOrder: "new", Keys: estimated, Records: records, Policy: policy, Rev: 77})
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
	if want := "Low confidence. Based on 1 finished medium change; a confident estimate needs 10. It took 4h. With one outcome, the 85th percentile cannot be read from it, so the band is open above 4h."; one.Explanation != want {
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
		"below the floor":         {Version: 1, MinSamples: 4, RecentSamples: 50, ForecastTrials: 2000},
		"fewer recent than needs": {Version: 1, MinSamples: 10, RecentSamples: 9, ForecastTrials: 2000},
		"no trials":               {Version: 1, MinSamples: 10, RecentSamples: 50},
	} {
		if bad.Check() == nil {
			t.Errorf("%s: %+v passed", name, bad)
		}
	}
}

// tierKeys repeats the tiers' keys beyond kind and size, so the test checks the estimator's choice of tier on its
// own terms.
var tierKeys = [][]func(a, b delivery.Keys) bool{
	{sameClass, sameRepository, sameAuthority, sameBucket},
	{sameClass, sameRepository, sameAuthority},
	{sameClass, sameAuthority},
	{sameAuthority},
	{},
}

func sameClass(a, b delivery.Keys) bool      { return a.CapabilityClass == b.CapabilityClass }
func sameRepository(a, b delivery.Keys) bool { return a.Repository == b.Repository }
func sameAuthority(a, b delivery.Keys) bool  { return a.MergeAuthority == b.MergeAuthority }
func sameBucket(a, b delivery.Keys) bool     { return bucket(a.Criteria) == bucket(b.Criteria) }

func bucket(n int64) int {
	switch {
	case n <= 0:
		return 0
	case n <= 3:
		return 1
	case n <= 7:
		return 2
	}
	return 3
}

// TestEveryEstimateTracesToTheRecordsItNames holds the estimator's promise on generated histories, with shared
// history, cut recent windows, and records that end together: each number of a band is a delivery time or cost
// at the rank the confidence gives among the records drawn on, every record named is a finished comparable one,
// the tier is the one the rules choose, and a low estimate from fewer than 6 records is open above.
func TestEveryEstimateTracesToTheRecordsItNames(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261114, 6))
	sizes, classes, repositories, authorities := []string{"small", "medium"}, []string{"implement", "review"}, []string{"repo-1", "repo-2"}, []string{"person_approves", "person_merges"}
	keysOf := func() delivery.Keys {
		return delivery.Keys{
			Kind: string(workorders.KindChange), Size: sizes[rng.IntN(2)], CapabilityClass: classes[rng.IntN(2)], Repository: repositories[rng.IntN(2)],
			MergeAuthority: authorities[rng.IntN(2)], Criteria: int64(rng.IntN(10)),
		}
	}
	for run := range 3000 {
		var records, pool []delivery.Record
		for i := range rng.IntN(40) {
			outcome := workorders.Done
			if rng.IntN(5) == 0 {
				outcome = workorders.Failed
			}
			records = append(records, delivery.Record{WorkOrder: fmt.Sprintf("wo-%02d", i), Keys: keysOf(), Outcome: outcome, Ended: int64(rng.IntN(20)),
				DeliveryMs: int64(1 + rng.IntN(100)), Usage: delivery.Usage{CostUSD: float64(rng.IntN(50))}})
		}
		for range rng.IntN(20) {
			k := keysOf()
			pool = append(pool, delivery.Record{Keys: delivery.Keys{Kind: k.Kind, Size: k.Size, CapabilityClass: k.CapabilityClass}, Outcome: workorders.Done,
				Ended: int64(rng.IntN(20)), DeliveryMs: int64(1 + rng.IntN(100)), Usage: delivery.Usage{CostUSD: float64(rng.IntN(50))}})
		}
		policy := delivery.EstimatePolicy{MinSamples: int64(5 + rng.IntN(6)), RecentSamples: int64(rng.IntN(15)), ForecastTrials: 1, ShareHistory: rng.IntN(2) == 0}
		window := max(policy.RecentSamples, policy.MinSamples)
		if policy.RecentSamples == 0 {
			window = 50
		}
		e := delivery.EstimateOf(delivery.EstimateInput{Keys: estimated, Records: records, Pool: pool, Policy: policy})
		counts := []int{}
		for _, same := range tierKeys {
			n := 0
			for _, r := range records {
				if r.Outcome == workorders.Done && r.Size == estimated.Size && !slices.ContainsFunc(same, func(f func(a, b delivery.Keys) bool) bool { return !f(r.Keys, estimated) }) {
					n++
				}
			}
			counts = append(counts, min(n, int(window)))
		}
		if policy.ShareHistory {
			n := 0
			for _, r := range slices.Concat(records, pool) {
				if r.Outcome == workorders.Done && r.Size == estimated.Size {
					n++
				}
			}
			counts = append(counts, min(n, int(window)))
		}
		want, confident := 0, false
		for i, n := range counts {
			if int64(n) >= policy.MinSamples {
				want, confident = i+1, true
				break
			}
		}
		if !confident {
			for i, n := range counts {
				if n > 0 && (want == 0 || n > counts[want-1]) {
					want = i + 1
				}
			}
		}
		if want == 0 {
			if e.Status != delivery.NoHistory || e.SampleCount != 0 || e.Confidence != "" {
				t.Fatalf("run %d: no history with %+v", run, e)
			}
			continue
		}
		if e.Tier != want || e.SampleCount != counts[want-1] || (e.Confidence == delivery.Confident) != confident || e.SampleCount != len(e.SampleIDs)+e.PoolSamples {
			t.Fatalf("run %d: tier %d of %d samples, want tier %d of %v", run, e.Tier, e.SampleCount, want, counts)
		}
		if e.Confidence == delivery.Low && e.SampleCount < 6 && !e.P85Open || e.P85Open && (e.Confidence != delivery.Low || e.SampleCount >= 6) {
			t.Fatalf("run %d: %d low samples open %v", run, e.SampleCount, e.P85Open)
		}
		byID := map[string]delivery.Record{}
		for _, r := range records {
			byID[r.WorkOrder] = r
		}
		for _, id := range e.SampleIDs {
			if r, ok := byID[id]; !ok || r.Outcome != workorders.Done || r.Size != estimated.Size {
				t.Fatalf("run %d: drew on %q, which is no finished comparable record", run, id)
			}
		}
		if e.PoolSamples > 0 || e.Explanation == "" {
			continue
		}
		var times []int64
		var costs []float64
		for _, id := range e.SampleIDs {
			times, costs = append(times, byID[id].DeliveryMs), append(costs, byID[id].Usage.CostUSD)
		}
		slices.Sort(times)
		slices.Sort(costs)
		n := len(times)
		p50, p85 := int(math.Ceil(0.5*float64(n)))-1, int(math.Ceil(0.85*float64(n)))-1
		if e.Confidence == delivery.Low {
			p50, p85 = int(math.Ceil(0.5*float64(n+1)))-1, min(int(math.Ceil(0.85*float64(n+1)))-1, n-1)
		}
		if e.DeliveryP50Ms != times[p50] || e.DeliveryP85Ms != times[p85] || e.CostP50USD != costs[p50] || e.CostP85USD != costs[p85] ||
			e.DeliveryMinMs != times[0] || e.DeliveryMaxMs != times[n-1] || e.CostMinUSD != costs[0] || e.CostMaxUSD != costs[n-1] {
			t.Fatalf("run %d: bands %+v not at their ranks among %v and %v", run, e, times, costs)
		}
	}
}

// TestACostBandReadsCostsOnTheirOwn sorts costs apart from times, so the p50 cost need not be the p50 record's.
func TestACostBandReadsCostsOnTheirOwn(t *testing.T) {
	var records []delivery.Record
	for i := range 10 {
		records = append(records, finished(fmt.Sprintf("r-%d", i), int64(i), int64(i+1)*hour, float64(100-10*i), nil))
	}
	e := estimateOf(records, delivery.DefaultEstimatePolicy())
	if e.Time() != (delivery.Band{P50: 5 * hour, P85: 9 * hour}) || e.Cost() != (delivery.CostBand{P50: 50, P85: 90}) || e.CostMinUSD != 10 || e.CostMaxUSD != 100 {
		t.Errorf("bands %+v %+v", e.Time(), e.Cost())
	}
}

// TestALowEstimateFromSixOrMoreIsNotOpen reads the p85 from the records once there are 6, under the minimum.
func TestALowEstimateFromSixOrMoreIsNotOpen(t *testing.T) {
	var records []delivery.Record
	for i := range 7 {
		records = append(records, finished(fmt.Sprintf("r-%d", i), int64(i), int64(i+1)*hour, 1, nil))
	}
	e := estimateOf(records, delivery.DefaultEstimatePolicy())
	// n = 7: p50 at rank 4, 4h; p85 at rank ceil(6.8) = 7, 7h.
	if e.Confidence != delivery.Low || e.P85Open || e.Time() != (delivery.Band{P50: 4 * hour, P85: 7 * hour}) || strings.Contains(e.Explanation, "open") {
		t.Errorf("seven records %+v", e)
	}
}

// TestTheExplanationSaysWhatItRestsOn covers the words of records that all took the same time, a pooled low
// estimate, and rework.
func TestTheExplanationSaysWhatItRestsOn(t *testing.T) {
	same := estimateOf([]delivery.Record{finished("a", 1, 2*hour, 1, nil), finished("b", 2, 2*hour, 1, nil)}, delivery.DefaultEstimatePolicy())
	if !strings.Contains(same.Explanation, " They each took 2h.") {
		t.Errorf("same times %q", same.Explanation)
	}
	sharing := delivery.DefaultEstimatePolicy()
	sharing.ShareHistory = true
	pooledLow := delivery.EstimateOf(delivery.EstimateInput{Keys: estimated, Pool: []delivery.Record{pooled(1, hour, 1), pooled(2, 3*hour, 1)}, Policy: sharing})
	want := "Low confidence. Based on 2 finished medium changes; a confident estimate needs 10. To find them, ropeshift ignored the capability class, the repository, " +
		"the merge authority level, and the number of criteria, and drew on the installation pool. They took from 1h to 3h. " +
		"With fewer than 6, the 85th percentile cannot be read from them, so the band is open above 3h."
	if pooledLow.Explanation != want {
		t.Errorf("pooled low\n got %q\nwant %q", pooledLow.Explanation, want)
	}
	rework := estimated
	rework.Kind = string(workorders.KindRework)
	r := delivery.EstimateOf(delivery.EstimateInput{Keys: rework, Records: []delivery.Record{finished("a", 1, hour, 1, func(k *delivery.Keys) { k.Kind = rework.Kind })}, Policy: delivery.DefaultEstimatePolicy()})
	if !strings.HasPrefix(r.Explanation, "Low confidence. Based on 1 finished medium rework;") {
		t.Errorf("rework %q", r.Explanation)
	}
	none := delivery.EstimateOf(delivery.EstimateInput{Keys: rework, Policy: delivery.DefaultEstimatePolicy()})
	if !strings.HasPrefix(none.Explanation, "No estimate: no medium rework has finished in this project yet") {
		t.Errorf("rework with no history %q", none.Explanation)
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

// TestDurationsReadDistinctlyAndCarry says neighbouring durations apart and carries a rounded unit into the next.
func TestDurationsReadDistinctlyAndCarry(t *testing.T) {
	const minute, second = 60_000, 1_000
	for ms, want := range map[int64]string{
		3*hour + 59*minute:    "3h59m",
		4*hour + 1*minute:     "4h1m",
		4 * hour:              "4h",
		hour - 1:              "1h",
		minute - 1:            "1m",
		59*minute + 30*second: "59m30s",
		45 * second:           "45s",
	} {
		e := estimateOf([]delivery.Record{finished("a", 1, ms, 1, nil)}, delivery.DefaultEstimatePolicy())
		if !strings.Contains(e.Explanation, "It took "+want+".") {
			t.Errorf("%d ms reads %q, want %q", ms, e.Explanation, want)
		}
	}
}

// TestAnEstimatePolicyVersionKeepsWhatAMemberWrote stores a policy version whose settings are what its body says,
// and refuses to read one whose body changed after its digest was taken.
func TestAnEstimatePolicyVersionKeepsWhatAMemberWrote(t *testing.T) {
	policy := delivery.EstimatePolicy{Version: 2, MinSamples: 12, RecentSamples: 60, ForecastTrials: 1000, ShareHistory: true}
	body := "[estimate_policy]\nversion = 2\n"
	version, err := delivery.NewEstimatePolicyVersion("id-1", "member-1", delivery.EstimatePolicyDraft{Body: body, Policy: policy}, 42)
	if err != nil || version.Kind != delivery.PolicyEstimate || version.Version != 2 || version.AuthoredBy != "member-1" || version.CreatedAt != 42 {
		t.Fatalf("version %+v: %v", version, err)
	}
	if read, err := delivery.ReadEstimatePolicy(version); err != nil || read != policy {
		t.Errorf("read %+v: %v", read, err)
	}
	version.Body += "share_history = false\n"
	if _, err := delivery.ReadEstimatePolicy(version); err == nil {
		t.Error("a body that changed after its digest read")
	}
	if (delivery.EstimatePolicy{MinSamples: 10, RecentSamples: 50, ForecastTrials: 1}).Check() == nil {
		t.Error("a policy with no version passed")
	}
}
