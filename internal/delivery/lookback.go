package delivery

import (
	"cmp"
	"math"
	"slices"

	"ropeshift.com/ropeshift/internal/workorders"
)

// The groupings look back reads delivery records by (FR-EST-5). ByProject is the one grouping above a
// project: it reads every active project's look back apart, because percentiles of separate projects do not
// combine.
const (
	ByKind       = "kind"
	BySize       = "size"
	ByAgent      = "agent"
	ByRepository = "repository"
	ByProject    = "project"
)

// trendWeeks is how many weeks a look back's trend covers, each ending a week after the one before it.
const trendWeeks = 12

// weekMs is one week.
const weekMs = int64(7 * 24 * 60 * 60 * 1000)

// Quantiles are a duration's 50th and 85th percentiles, by nearest rank.
type Quantiles struct {
	P50 int64 `json:"p50"`
	P85 int64 `json:"p85"`
}

// CostQuantiles are estimated cost's 50th and 85th percentiles, by nearest rank.
type CostQuantiles struct {
	P50 float64 `json:"p50"`
	P85 float64 `json:"p85"`
}

// Week is one week of a look back's trend: the work that reached `done` in it, and its delivery time.
type Week struct {
	From     int64      `json:"from"`
	Done     int64      `json:"done"`
	Delivery *Quantiles `json:"delivery,omitempty"`
}

// Group is look back over one group of delivery records. Done, Failed, and Canceled count records by outcome,
// and only the work that reached `done` counts in the durations, the cost, the throughput, and the share of
// elapsed time in each segment kind. A duration or cost with no work done is absent, never zero.
// CostLowerBound says some cost was never reported.
type Group struct {
	Key            string           `json:"key"`
	Done           int64            `json:"done"`
	Failed         int64            `json:"failed"`
	Canceled       int64            `json:"canceled"`
	Elapsed        *Quantiles       `json:"elapsed,omitempty"`
	Delivery       *Quantiles       `json:"delivery,omitempty"`
	Cost           *CostQuantiles   `json:"cost,omitempty"`
	CostLowerBound bool             `json:"cost_lower_bound,omitempty"`
	PerWeek        float64          `json:"per_week"`
	Share          map[Kind]float64 `json:"share"`
	Trend          []Week           `json:"trend"`
}

// LookBack is delivery look back over a window, from From up to To, for all work first and then each group
// in order of its key.
type LookBack struct {
	By     string  `json:"by"`
	From   int64   `json:"from"`
	To     int64   `json:"to"`
	Groups []Group `json:"groups"`
}

// LookBackOf reads delivery records over a window, grouped by one key (FR-EST-5). The trend covers the twelve
// weeks up to To, whatever the window.
func LookBackOf(records []Record, by string, from, to int64) LookBack {
	out := LookBack{By: by, From: from, To: to, Groups: []Group{groupOf("", records, from, to)}}
	byKey := map[string][]Record{}
	for _, r := range records {
		byKey[keyOf(r, by)] = append(byKey[keyOf(r, by)], r)
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		out.Groups = append(out.Groups, groupOf(key, byKey[key], from, to))
	}
	return out
}

func keyOf(r Record, by string) string {
	switch by {
	case BySize:
		return r.Size
	case ByAgent:
		return r.Agent
	case ByRepository:
		return r.Repository
	}
	return r.Kind
}

func groupOf(key string, records []Record, from, to int64) Group {
	out := Group{Key: key, Share: map[Kind]float64{}, Trend: make([]Week, 0, trendWeeks)}
	var done []Record
	for _, r := range records {
		if r.Ended < from || r.Ended >= to {
			continue
		}
		switch r.Outcome {
		case workorders.Done:
			done = append(done, r)
		case workorders.Failed:
			out.Failed++
		case workorders.Canceled:
			out.Canceled++
		}
	}
	out.Done = int64(len(done))
	if weeks := float64(to-from) / float64(weekMs); weeks > 0 {
		out.PerWeek = float64(out.Done) / weeks
	}
	if len(done) > 0 {
		elapsed, delivery := quantiles(done, func(r Record) int64 { return r.ElapsedMs }), quantiles(done, func(r Record) int64 { return r.DeliveryMs })
		out.Elapsed, out.Delivery = &elapsed, &delivery
		costs := make([]float64, 0, len(done))
		var total int64
		for _, r := range done {
			costs = append(costs, r.Usage.CostUSD)
			out.CostLowerBound = out.CostLowerBound || r.Usage.CostLowerBound
			total += r.ElapsedMs
			for kind, ms := range r.ByKind {
				out.Share[kind] += float64(ms)
			}
		}
		slices.Sort(costs)
		out.Cost = &CostQuantiles{P50: costs[rank(len(costs), 0.5)], P85: costs[rank(len(costs), 0.85)]}
		for kind := range out.Share {
			if total > 0 {
				out.Share[kind] /= float64(total)
			}
		}
	}
	for i := range trendWeeks {
		start := to - int64(trendWeeks-i)*weekMs
		w := Week{From: start}
		var in []Record
		for _, r := range records {
			if r.Outcome == workorders.Done && r.Ended >= start && r.Ended < start+weekMs {
				in = append(in, r)
			}
		}
		if w.Done = int64(len(in)); w.Done > 0 {
			q := quantiles(in, func(r Record) int64 { return r.DeliveryMs })
			w.Delivery = &q
		}
		out.Trend = append(out.Trend, w)
	}
	return out
}

// quantiles are the 50th and 85th percentiles of one duration of some records, by nearest rank.
func quantiles(records []Record, of func(Record) int64) Quantiles {
	values := make([]int64, 0, len(records))
	for _, r := range records {
		values = append(values, of(r))
	}
	slices.SortFunc(values, cmp.Compare[int64])
	return Quantiles{P50: values[rank(len(values), 0.5)], P85: values[rank(len(values), 0.85)]}
}

// rank is the index of a percentile by nearest rank among n sorted values.
func rank(n int, p float64) int { return max(0, min(int(math.Ceil(p*float64(n)))-1, n-1)) }
