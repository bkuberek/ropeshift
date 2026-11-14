package delivery

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/workorders"
)

// EstimatorVersion is the version of the estimator. An estimate records the version that made it, and
// calibration reads its outcomes by version.
const EstimatorVersion = 1

// EstimatePolicy is a project's `[estimate_policy]` (ADR-0026): how many comparable records a confident
// estimate needs, never fewer than MinSamplesFloor; how many of the most recent comparable records it reads;
// how many trials a forecast runs; and whether the project shares its delivery history with the
// installation's pool. Only a member changes it, and no calibration process writes it.
type EstimatePolicy struct {
	MinSamples     int64 `toml:"min_samples" json:"min_samples"`
	RecentSamples  int64 `toml:"recent_samples" json:"recent_samples"`
	ForecastTrials int64 `toml:"forecast_trials" json:"forecast_trials"`
	ShareHistory   bool  `toml:"share_history" json:"share_history"`
}

// MinSamplesFloor is the least min_samples a policy may set.
const MinSamplesFloor = 5

// ErrBadEstimatePolicy refuses an estimate policy that does not read.
var ErrBadEstimatePolicy = errors.New("invalid estimate policy")

// DefaultEstimatePolicy is the policy of a project that sets none.
func DefaultEstimatePolicy() EstimatePolicy {
	return EstimatePolicy{MinSamples: 10, RecentSamples: 50, ForecastTrials: 2000}
}

// Check refuses a confident estimate on fewer than MinSamplesFloor records, a read of fewer recent records
// than a confident estimate needs, and a forecast of no trials.
func (p EstimatePolicy) Check() error {
	switch {
	case p.MinSamples < MinSamplesFloor:
		return fmt.Errorf("%w: min_samples is at least %d", ErrBadEstimatePolicy, MinSamplesFloor)
	case p.RecentSamples < p.MinSamples:
		return fmt.Errorf("%w: recent_samples is at least min_samples", ErrBadEstimatePolicy)
	case p.ForecastTrials < 1:
		return fmt.Errorf("%w: forecast_trials is at least 1", ErrBadEstimatePolicy)
	}
	return nil
}

// Confidence is how far an estimate can be trusted from what it rests on (FR-EST-6, FR-EST-7).
type Confidence string

// The confidences. A confident estimate rests on at least min_samples comparable records; a low one on fewer;
// and an estimate with no history rests on none and has no number.
const (
	Confident Confidence = "confident"
	Low       Confidence = "low"
	NoHistory Confidence = "no_history"
)

// The keys beyond kind and size a tier may match on, in the order the tiers drop them back to front.
const (
	KeyCapabilityClass = "capability_class"
	KeyRepository      = "repository"
	KeyMergeAuthority  = "merge_authority"
	KeyCriteria        = "criteria"
)

// tiers lists each tier's keys beyond kind and size, narrowest first. Tier 6 matches kind and size in the
// installation's pool.
var tiers = [][]string{
	{KeyCapabilityClass, KeyRepository, KeyMergeAuthority, KeyCriteria},
	{KeyCapabilityClass, KeyRepository, KeyMergeAuthority},
	{KeyCapabilityClass, KeyMergeAuthority},
	{KeyMergeAuthority},
	{},
}

// poolTier is the tier that draws on the installation's pool.
const poolTier = 6

// Band is a delivery time's band: the 50th and 85th percentiles of the comparable records.
type Band struct {
	P50 int64 `json:"p50"`
	P85 int64 `json:"p85"`
}

// CostBand is an estimated cost's band, at list price.
type CostBand struct {
	P50 float64 `json:"p50"`
	P85 float64 `json:"p85"`
}

// Estimate is one work order's band of delivery time and estimated cost, and everything it rests on, so a
// person can trace each number to the work it came from (FR-EST-6, FR-EST-7, FR-EST-14). Matched and Dropped
// name the keys beyond kind and size the tier matched and those it set aside; Drawn names the work orders of
// the project it drew on, newest first, and Pooled says it drew on the installation's pool, whose records
// name no work order. P85Open says no record bounds 85 percent of outcomes, so the p85 is the longest record
// and reads as more than it; CostAtLeast says a record's cost was a lower bound. Explanation is the text every
// surface shows.
type Estimate struct {
	WorkOrder        string     `json:"work_order,omitempty"`
	Confidence       Confidence `json:"confidence"`
	Tier             int        `json:"tier,omitempty"`
	Matched          []string   `json:"matched"`
	Dropped          []string   `json:"dropped"`
	Samples          int        `json:"samples"`
	MinSamples       int64      `json:"min_samples"`
	Drawn            []string   `json:"drawn"`
	Pooled           bool       `json:"pooled,omitempty"`
	ShortestMs       int64      `json:"shortest_ms,omitempty"`
	LongestMs        int64      `json:"longest_ms,omitempty"`
	Time             *Band      `json:"time,omitempty"`
	Cost             *CostBand  `json:"cost,omitempty"`
	P85Open          bool       `json:"p85_open,omitempty"`
	CostAtLeast      bool       `json:"cost_at_least,omitempty"`
	Rev              int64      `json:"rev"`
	EstimatorVersion int        `json:"estimator_version"`
	PolicyVersion    int64      `json:"policy_version"`
	Explanation      string     `json:"explanation"`
}

// EstimateInput is what one estimate is made from: the keys of the work order being estimated, which are known
// before it starts; the project's delivery records; the installation's pool, read only when the policy shares
// history; the policy and its version; and the revision of the ledger the records were read at.
type EstimateInput struct {
	WorkOrder     string
	Keys          Keys
	Records       []Record
	Pool          []Record
	Policy        EstimatePolicy
	PolicyVersion int64
	Rev           int64
}

// EstimateOf estimates one `change` or `rework` work order from comparable finished work (ADR-0026). It reads
// the tiers narrowest first, each from the policy's most recent comparable records that ended `done`. The
// first tier with at least min_samples gives a confident band at the 50th and 85th percentiles by nearest
// rank. Below that at every tier, the tier with the most records, the narrowest among equals, gives a low
// confidence band at the records of rank ceil(0.5 × (n + 1)) and ceil(0.85 × (n + 1)); a p85 rank past n leaves
// the band open above the longest record. With no record at any tier there is no number. The estimate
// explains itself in plain words from its own fields.
func EstimateOf(in EstimateInput) Estimate {
	out := Estimate{
		WorkOrder: in.WorkOrder, Confidence: NoHistory, Matched: []string{}, Dropped: []string{}, Drawn: []string{}, MinSamples: in.Policy.MinSamples,
		Rev: in.Rev, EstimatorVersion: EstimatorVersion, PolicyVersion: in.PolicyVersion,
	}
	candidates := make([][]Record, 0, len(tiers)+1)
	for _, keys := range tiers {
		candidates = append(candidates, comparable(in.Records, in.Keys, keys, in.Policy.RecentSamples))
	}
	if in.Policy.ShareHistory {
		candidates = append(candidates, comparable(in.Pool, in.Keys, nil, in.Policy.RecentSamples))
	}
	chosen := -1
	for i, records := range candidates {
		if int64(len(records)) >= in.Policy.MinSamples {
			chosen, out.Confidence = i, Confident
			break
		}
	}
	if chosen < 0 {
		for i, records := range candidates {
			if len(records) > 0 && (chosen < 0 || len(records) > len(candidates[chosen])) {
				chosen, out.Confidence = i, Low
			}
		}
	}
	if chosen < 0 {
		out.Explanation = explain(out, in.Keys, in.Policy)
		return out
	}
	records := candidates[chosen]
	out.Tier, out.Samples, out.Pooled = chosen+1, len(records), chosen+1 == poolTier
	matched := []string{}
	if !out.Pooled {
		matched = tiers[chosen]
	}
	out.Matched = slices.Clone(matched)
	for _, key := range tiers[0] {
		if !slices.Contains(matched, key) {
			out.Dropped = append(out.Dropped, key)
		}
	}
	times := make([]int64, 0, len(records))
	costs := make([]float64, 0, len(records))
	for _, r := range records {
		if !out.Pooled {
			out.Drawn = append(out.Drawn, r.WorkOrder)
		}
		times = append(times, r.DeliveryMs)
		costs = append(costs, r.Usage.CostUSD)
		out.CostAtLeast = out.CostAtLeast || r.Usage.CostLowerBound
	}
	slices.Sort(times)
	slices.Sort(costs)
	out.ShortestMs, out.LongestMs = times[0], times[len(times)-1]
	p50, p85 := rank(len(times), 0.5), rank(len(times), 0.85)
	if out.Confidence == Low {
		p50, out.P85Open = lowRank(len(times), 0.5), lowRank(len(times), 0.85) >= len(times)
		p85 = min(lowRank(len(times), 0.85), len(times)-1)
	}
	out.Time = &Band{P50: times[p50], P85: times[p85]}
	out.Cost = &CostBand{P50: costs[p50], P85: costs[p85]}
	out.Explanation = explain(out, in.Keys, in.Policy)
	return out
}

// comparable is the most recent records that ended `done` with the same kind and size and the same values of
// some more keys, newest by end first.
func comparable(records []Record, want Keys, keys []string, recent int64) []Record {
	var out []Record
	for _, r := range records {
		if r.Outcome != workorders.Done || r.Kind != want.Kind || r.Size != want.Size {
			continue
		}
		if !slices.ContainsFunc(keys, func(key string) bool { return valueOf(r.Keys, key) != valueOf(want, key) }) {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b Record) int {
		return cmp.Or(cmp.Compare(b.Ended, a.Ended), cmp.Compare(a.WorkOrder, b.WorkOrder))
	})
	return out[:min(len(out), int(max(recent, 0)))]
}

// valueOf is one key's value of a work order, with the number of criteria read as its bucket.
func valueOf(k Keys, key string) string {
	switch key {
	case KeyCapabilityClass:
		return k.CapabilityClass
	case KeyRepository:
		return k.Repository
	case KeyMergeAuthority:
		return k.MergeAuthority
	}
	return criteriaBucket(k.Criteria)
}

// criteriaBucket groups the number of criteria: 1 to 3, 4 to 7, and 8 or more. A contract with no criteria is a
// bucket of its own.
func criteriaBucket(n int64) string {
	switch {
	case n <= 0:
		return "none"
	case n <= 3:
		return "1-3"
	case n <= 7:
		return "4-7"
	}
	return "8+"
}

// lowRank is the index of the record at rank ceil(p × (n + 1)) of n sorted records, which may be past the last.
func lowRank(n int, p float64) int { return int(math.Ceil(p*float64(n+1))) - 1 }

// openBelow is the number of records under which a low-confidence p85 is open: below 6, ceil(0.85 × (n + 1)) is
// past n.
const openBelow = 6

// keyWords says a key as a person reads it.
var keyWords = map[string]string{
	KeyCapabilityClass: "capability class",
	KeyRepository:      "repository",
	KeyMergeAuthority:  "merge authority level",
	KeyCriteria:        "number of criteria",
}

// explain writes an estimate's explanation from its own fields (FR-EST-14). A confident estimate names its
// basis; a low one adds how many outcomes it used against the minimum, which keys it set aside to find them,
// and how far apart they were; an estimate with no history says what is missing.
func explain(e Estimate, keys Keys, policy EstimatePolicy) string {
	work := func(n int) string { return workWords(keys, n) }
	switch e.Confidence {
	case NoHistory:
		text := "No estimate: no " + work(1) + " has finished in this project yet"
		if policy.ShareHistory {
			return text + " or in the installation pool."
		}
		return text + ", and this project does not share history with other projects."
	case Confident:
		text := fmt.Sprintf("Based on %d finished %s", e.Samples, work(e.Samples))
		switch {
		case e.Pooled:
			return text + " in the installation pool."
		case len(e.Matched) > 0:
			words := make([]string, 0, len(e.Matched))
			for _, key := range e.Matched {
				words = append(words, keyWords[key])
			}
			return text + " with the same " + list(words) + "."
		}
		return text + "."
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Low confidence. Based on %d finished %s; a confident estimate needs %d.", e.Samples, work(e.Samples), e.MinSamples)
	if len(e.Dropped) > 0 || e.Pooled {
		words := make([]string, 0, len(e.Dropped))
		for _, key := range e.Dropped {
			words = append(words, "the "+keyWords[key])
		}
		fmt.Fprintf(&out, " To find them, ropeshift ignored %s", list(words))
		if e.Pooled {
			out.WriteString(", and drew on the installation pool")
		}
		out.WriteString(".")
	}
	switch {
	case e.Samples == 1:
		fmt.Fprintf(&out, " It took %s.", durationWords(e.ShortestMs))
	case e.ShortestMs == e.LongestMs:
		fmt.Fprintf(&out, " They each took %s.", durationWords(e.ShortestMs))
	default:
		fmt.Fprintf(&out, " They took from %s to %s.", durationWords(e.ShortestMs), durationWords(e.LongestMs))
	}
	if e.P85Open {
		fmt.Fprintf(&out, " With fewer than %d, the 85th percentile cannot be read from them, so the band is open above %s.", openBelow, durationWords(e.LongestMs))
	}
	return out.String()
}

// workWords names n finished work orders of an estimate's kind and size, such as "medium changes".
func workWords(keys Keys, n int) string {
	noun := keys.Kind
	if n != 1 {
		noun += "s"
	}
	if keys.Size == "" || keys.Size == "unknown" {
		return noun + " of unknown size"
	}
	return keys.Size + " " + noun
}

// list joins words as a sentence does: "a", "a and b", "a, b, and c".
func list(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	case 2:
		return words[0] + " and " + words[1]
	}
	return strings.Join(words[:len(words)-1], ", ") + ", and " + words[len(words)-1]
}

// durationWords says a duration as a person reads it: hours, minutes, or seconds.
func durationWords(ms int64) string {
	const minute, hour = 60_000, 3_600_000
	switch {
	case ms >= hour:
		return strings.TrimSuffix(strconv.FormatFloat(math.Round(float64(ms)/hour*10)/10, 'f', 1, 64), ".0") + "h"
	case ms >= minute:
		return strconv.FormatInt((ms+minute/2)/minute, 10) + "m"
	}
	return strconv.FormatInt((ms+500)/1000, 10) + "s"
}
