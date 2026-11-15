package delivery

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
)

// Progress is what an unfinished work order has had so far, which a forecast keeps the remainder of longer work
// beyond (FR-EST-8): whether it started, the delivery time it has had, which leaves out its waits for a lane, the
// lane time its lanes and its review lanes held, and what they cost. Work that only waited for a lane has not
// started.
type Progress struct {
	Started    bool
	DeliveryMs int64
	LaneMs     int64
	CostUSD    float64
}

// ProgressOf reads an unfinished timeline as RecordOf reads a finished one, with the review timelines and the
// usage of facts.
func ProgressOf(t Timeline, f Facts) Progress {
	var out Progress
	for _, s := range t.Segments {
		if s.Kind != Queued {
			out.DeliveryMs += s.To - s.From
		}
		out.LaneMs += laneTime(s)
	}
	for _, review := range f.Reviews {
		for _, s := range review.Segments {
			out.LaneMs += laneTime(s)
		}
	}
	out.CostUSD = f.Usage.CostUSD
	out.Started = out.DeliveryMs > 0 || out.LaneMs > 0
	return out
}

// PersonMedianMs is the median wait for a person, by nearest rank, among the policy's most recent delivery records,
// newest by end, or zero when they hold none: what a forecast's what-if cuts each wait for a person to (FR-EST-9).
func PersonMedianMs(records []Record, policy EstimatePolicy) int64 {
	recent := slices.Clone(records)
	slices.SortStableFunc(recent, func(a, b Record) int {
		return cmp.Or(cmp.Compare(b.Ended, a.Ended), cmp.Compare(a.WorkOrder, b.WorkOrder))
	})
	recent = recent[:min(len(recent), int(policy.effective().RecentSamples))]
	var waits []int64
	for _, r := range recent {
		waits = append(waits, r.PersonWaits...)
	}
	if len(waits) == 0 {
		return 0
	}
	slices.Sort(waits)
	return waits[rank(len(waits), 0.5)]
}

// ForecastDigest is the digest of what a forecast is made from, apart from what moves with every event: the
// revision, the time it runs from, and the time and cost started work has had so far, and apart from where and
// when each estimate was made. A tick that finds the digest of the forecast it kept last has nothing new to
// forecast but the passing time.
func ForecastDigest(in ForecastInput) string {
	in.Rev, in.Now = 0, 0
	in.Work = slices.Clone(in.Work)
	for i := range in.Work {
		w := &in.Work[i]
		w.SoFarMs, w.LaneSoFarMs, w.SpentUSD = 0, 0, 0
		w.Estimate = w.Estimate.said()
		w.Estimate.Rev = 0
	}
	slices.SortStableFunc(in.Work, func(a, b ForecastWork) int { return cmp.Compare(a.WorkOrder, b.WorkOrder) })
	// The input is plain data, so it always encodes.
	body, _ := json.Marshal(in)
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
