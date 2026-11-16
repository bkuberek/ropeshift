package delivery_test

import (
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// historic is a done delivery record of a kind, size, capability class, and repository, with the durations, cost,
// rounds, and change size a history group reads.
func historic(id, kind, size, class, repository string, ended, deliveryMs, laneMs int64, cost float64, rounds int64, change *ledger.ChangeSize) delivery.Record {
	return delivery.Record{
		WorkOrder: id, Keys: delivery.Keys{Kind: kind, Size: size, CapabilityClass: class, Repository: repository, MergeAuthority: "person_approves", Criteria: 2},
		Outcome: workorders.Done, Ended: ended, DeliveryMs: deliveryMs, LaneMs: laneMs, Usage: delivery.Usage{CostUSD: cost}, Rounds: rounds, Change: change,
	}
}

// aHistory is twelve medium changes of the implement class, ten in repo-1 and two in repo-2, and four large ones in
// repo-1, with one failed and one canceled change beside them.
func aHistory() []delivery.Record {
	var records []delivery.Record
	for i := int64(1); i <= 10; i++ {
		rounds := int64(1)
		if i > 5 {
			rounds = 2
		}
		records = append(records, historic(fmt.Sprintf("m-%02d", i), "change", "medium", "implement", "repo-1", i, i*hour, i*hour/2, float64(i), rounds,
			&ledger.ChangeSize{Files: i, Added: 10 * i, Removed: i}))
	}
	records = append(records,
		historic("m-11", "change", "medium", "implement", "repo-2", 11, 3*hour, hour, 3, 1, nil),
		historic("m-12", "change", "medium", "implement", "repo-2", 12, 7*hour, 2*hour, 7, 1, nil),
	)
	for i, h := range []int64{2, 5, 8, 11} {
		records = append(records, historic(fmt.Sprintf("l-%d", i), "change", "large", "implement", "repo-1", int64(20+i), h*hour, h*hour/2, float64(h), 1, nil))
	}
	failed := historic("failed", "change", "medium", "implement", "repo-1", 30, 99*hour, 99*hour, 99, 5, nil)
	failed.Outcome = workorders.Failed
	canceled := historic("canceled", "change", "medium", "implement", "repo-1", 31, 98*hour, 98*hour, 98, 5, nil)
	canceled.Outcome = workorders.Canceled
	return append(records, failed, canceled)
}

// TestHistoryAggregatesFinishedWorkByKindAndSize holds FR-EST-12: each group has its count, delivery time, lane
// time, and cost at p50 and p85 by nearest rank, the mean rounds, and the median change size, from work that
// ended `done` alone, in order of kind and size.
func TestHistoryAggregatesFinishedWorkByKindAndSize(t *testing.T) {
	h := delivery.HistoryOf(aHistory(), delivery.DefaultEstimatePolicy())
	if len(h.ByKindSize) != 2 || h.ByKindSize[0].Size != "large" || h.ByKindSize[1].Size != "medium" {
		t.Fatalf("groups by kind and size %+v", h.ByKindSize)
	}
	medium := h.ByKindSize[1]
	if medium.Kind != "change" || medium.CapabilityClass != "" || medium.Repository != "" || medium.Count != 12 {
		t.Fatalf("medium %+v", medium)
	}
	// Twelve delivery times, 1h to 10h and 3h and 7h: sorted 1 2 3 3 4 5 6 7 7 8 9 10, so p50 is the 6th, p85 the 11th.
	if medium.Delivery != (delivery.Quantiles{P50: 5 * hour, P85: 9 * hour}) {
		t.Errorf("delivery %+v", medium.Delivery)
	}
	if medium.Lane != (delivery.Quantiles{P50: 2 * hour, P85: 4*hour + hour/2}) {
		t.Errorf("lane %+v", medium.Lane)
	}
	if medium.Cost != (delivery.CostQuantiles{P50: 5, P85: 9}) || medium.CostLowerBound {
		t.Errorf("cost %+v", medium.Cost)
	}
	// Five records of one round, five of two, and two of one.
	if want := 17.0 / 12.0; medium.MeanRounds < want-1e-9 || medium.MeanRounds > want+1e-9 {
		t.Errorf("mean rounds %v, want %v", medium.MeanRounds, want)
	}
	// Ten records carry a change size, of 1 to 10 files: the median is the 5th.
	if medium.Change == nil || *medium.Change != (delivery.ChangeMedian{Measured: 10, Files: 5, Added: 50, Removed: 5}) {
		t.Errorf("change %+v", medium.Change)
	}
	if large := h.ByKindSize[0]; large.Count != 4 || large.Change != nil || large.Delivery.P85 != 11*hour {
		t.Errorf("large %+v", large)
	}
}

// TestHistoryAlsoGroupsByCapabilityClassAndRepository holds the second grouping of FR-EST-12.
func TestHistoryAlsoGroupsByCapabilityClassAndRepository(t *testing.T) {
	h := delivery.HistoryOf(aHistory(), delivery.DefaultEstimatePolicy())
	var got []string
	for _, g := range h.ByKindSizeClassRepository {
		got = append(got, fmt.Sprintf("%s %s %s %s %d", g.Kind, g.Size, g.CapabilityClass, g.Repository, g.Count))
	}
	want := []string{"change large implement repo-1 4", "change medium implement repo-1 10", "change medium implement repo-2 2"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("groups %v, want %v", got, want)
	}
	if repo2 := h.ByKindSizeClassRepository[2]; repo2.Delivery != (delivery.Quantiles{P50: 3 * hour, P85: 7 * hour}) || repo2.Change != nil {
		t.Errorf("repo-2 %+v", repo2)
	}
}

// TestAGroupUnderMinSamplesIsLowWithTheExplanationAnEstimateWouldCarry holds FR-EST-12 and FR-EST-14: a group of
// fewer finished work orders than min_samples carries the low label and the words an estimate from just those
// records carries, naming the keys the group set aside; a group at the minimum is confident.
func TestAGroupUnderMinSamplesIsLowWithTheExplanationAnEstimateWouldCarry(t *testing.T) {
	h := delivery.HistoryOf(aHistory(), delivery.DefaultEstimatePolicy())
	large := h.ByKindSize[0]
	if large.Confidence != delivery.Low {
		t.Errorf("four large changes are %q", large.Confidence)
	}
	const wantLarge = "Low confidence. Based on 4 finished large changes; a confident estimate needs 10. " +
		"To find them, ropeshift ignored the capability class, the repository, the merge authority level, and the number of criteria. " +
		"They took from 2h to 11h. With fewer than 6, the 85th percentile cannot be read from them, so the band is open above 11h."
	if large.Explanation != wantLarge {
		t.Errorf("explanation\n got %q\nwant %q", large.Explanation, wantLarge)
	}
	if medium := h.ByKindSize[1]; medium.Confidence != delivery.Confident || medium.Explanation != "Based on 12 finished medium changes." {
		t.Errorf("twelve medium changes: %q, %q", medium.Confidence, medium.Explanation)
	}
	// The narrower grouping set aside only what it did not match on.
	repo2 := h.ByKindSizeClassRepository[2]
	const wantRepo2 = "Low confidence. Based on 2 finished medium changes; a confident estimate needs 10. " +
		"To find them, ropeshift ignored the merge authority level and the number of criteria. " +
		"They took from 3h to 7h. With fewer than 6, the 85th percentile cannot be read from them, so the band is open above 7h."
	if repo2.Confidence != delivery.Low || repo2.Explanation != wantRepo2 {
		t.Errorf("repo-2: %q, %q", repo2.Confidence, repo2.Explanation)
	}
	if repo1 := h.ByKindSizeClassRepository[1]; repo1.Confidence != delivery.Confident ||
		repo1.Explanation != "Based on 10 finished medium changes with the same capability class and repository." {
		t.Errorf("repo-1: %q, %q", repo1.Confidence, repo1.Explanation)
	}
}

// TestHistoryFollowsThePolicysMinimumAndWindow holds a group to the policy the estimator runs under: the minimum
// decides the label, and each group reads only its most recent recent_samples records, newest by end.
func TestHistoryFollowsThePolicysMinimumAndWindow(t *testing.T) {
	policy := delivery.DefaultEstimatePolicy()
	policy.MinSamples, policy.RecentSamples = 5, 6
	h := delivery.HistoryOf(aHistory(), policy)
	large, medium := h.ByKindSize[0], h.ByKindSize[1]
	if large.Confidence != delivery.Low || large.Count != 4 {
		t.Errorf("large %+v", large)
	}
	// The six newest of the twelve medium changes ended 7 to 12: delivery 7h 8h 9h 10h and 3h 7h.
	if medium.Confidence != delivery.Confident || medium.Count != 6 || medium.Delivery.P50 != 7*hour || medium.Delivery.P85 != 10*hour {
		t.Errorf("medium %+v", medium)
	}
}

// TestHistoryOfNothingHasNoGroups says a project with no finished work has no group, so a lane reads an empty
// history and never a made-up one.
func TestHistoryOfNothingHasNoGroups(t *testing.T) {
	failed := historic("f", "change", "small", "implement", "repo-1", 1, hour, hour, 1, 1, nil)
	failed.Outcome = workorders.Failed
	for _, records := range [][]delivery.Record{nil, {failed}} {
		h := delivery.HistoryOf(records, delivery.DefaultEstimatePolicy())
		if len(h.ByKindSize) != 0 || len(h.ByKindSizeClassRepository) != 0 {
			t.Errorf("history of %d records has groups %+v", len(records), h)
		}
		if text := h.Text(nil); !strings.Contains(text, "No work has finished in this project yet") {
			t.Errorf("empty history reads %q", text)
		}
	}
}

// TestACostWithALowerBoundReadsAtLeast holds FR-EST-7's cost wording for a group.
func TestACostWithALowerBoundReadsAtLeast(t *testing.T) {
	r := historic("a", "change", "small", "implement", "repo-1", 1, hour, hour, 2, 1, nil)
	r.Usage.CostLowerBound = true
	h := delivery.HistoryOf([]delivery.Record{r}, delivery.DefaultEstimatePolicy())
	if !h.ByKindSize[0].CostLowerBound || !strings.Contains(h.Text(nil), "cost p50 at least $2.00") {
		t.Errorf("a lower-bound cost reads %q", h.Text(nil))
	}
}

// TestHistoryTextNamesEachGroupWithItsLabel holds what a planning lane reads: every group on a line of its own, the
// repository by name where the project has one, the low label and its explanation under a thin group, and the
// notice that the figures are history and no promise.
func TestHistoryTextNamesEachGroupWithItsLabel(t *testing.T) {
	text := delivery.HistoryOf(aHistory(), delivery.DefaultEstimatePolicy()).Text(map[string]string{"repo-1": "api", "repo-2": "web"})
	for _, want := range []string{
		"### By kind and size",
		"### By kind, size, capability class, and repository",
		"- change, medium: 12 finished; delivery p50 5h, p85 9h; lane time p50 2h, p85 4h30m; cost p50 $5.00, p85 $9.00; " +
			"mean 1.4 rounds; median change 5 files, +50 -5 lines, of 10 measured. Confident. Based on 12 finished medium changes.",
		"- change, large: 4 finished; delivery p50 5h, p85 11h;",
		"; no change size recorded. Low confidence. Based on 4 finished large changes;",
		"- change, medium, implement, api: 10 finished;",
		"- change, medium, implement, web: 2 finished;",
		"history and no promise",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the history does not say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "repo-1") || strings.Contains(text, "99") {
		t.Errorf("the history names a repository ID or counts work that did not finish:\n%s", text)
	}
}
