package delivery

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/measures"
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
	Version        int64 `toml:"version" json:"version"`
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
	return EstimatePolicy{Version: 1, MinSamples: 10, RecentSamples: 50, ForecastTrials: 2000}
}

// Check refuses a version below 1, a confident estimate on fewer than MinSamplesFloor records, a read of fewer
// recent records than a confident estimate needs, and a forecast of no trials.
func (p EstimatePolicy) Check() error {
	switch {
	case p.Version < 1:
		return fmt.Errorf("%w: version %d", ErrBadEstimatePolicy, p.Version)
	case p.MinSamples < MinSamplesFloor:
		return fmt.Errorf("%w: min_samples is at least %d", ErrBadEstimatePolicy, MinSamplesFloor)
	case p.RecentSamples < p.MinSamples:
		return fmt.Errorf("%w: recent_samples is at least min_samples", ErrBadEstimatePolicy)
	case p.ForecastTrials < 1:
		return fmt.Errorf("%w: forecast_trials is at least 1", ErrBadEstimatePolicy)
	}
	return nil
}

// Status says whether an estimate has a band: `estimated`, or `no_history` when no comparable work ever
// finished, which has no number.
type Status string

// The statuses.
const (
	Estimated Status = "estimated"
	NoHistory Status = "no_history"
)

// effective is a policy as the estimator applies it, whatever it was handed: an unset minimum or window takes
// its default, a minimum below the floor rises to it, and a window narrower than the minimum widens to it.
func (p EstimatePolicy) effective() EstimatePolicy {
	defaults := DefaultEstimatePolicy()
	if p.MinSamples <= 0 {
		p.MinSamples = defaults.MinSamples
	}
	p.MinSamples = max(p.MinSamples, MinSamplesFloor)
	if p.RecentSamples <= 0 {
		p.RecentSamples = defaults.RecentSamples
	}
	p.RecentSamples = max(p.RecentSamples, p.MinSamples)
	return p
}

// Confidence is how far an estimated band can be trusted from what it rests on (FR-EST-6, FR-EST-7): confident
// on at least min_samples comparable records, low on fewer. An estimate with no history has none.
type Confidence string

// The confidences.
const (
	Confident Confidence = "confident"
	Low       Confidence = "low"
)

// The keys a tier may match on. Every tier matches kind and size; the rest it may drop, back to front.
const (
	KeyKind            = "kind"
	KeySize            = "size"
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
// person can trace each number to the work it came from (FR-EST-6, FR-EST-7, FR-EST-14). Its fields follow the
// estimate's data model. Keys are the keys its tier matched, kind and size always among them, and DroppedKeys
// those it set aside. SampleIDs names the work orders of the project it drew on, newest first, and PoolSamples
// counts the records of other projects it drew from the installation's pool, which name no work order. The
// band holds the 50th and 85th percentiles and the least and most of what it drew on, for time and cost apart.
// P85Open says no record bounds 85 percent of outcomes, so the p85 is the longest record and reads as more than
// it; CostAtLeast says a record's cost was a lower bound. Explanation is the text every surface shows. A kept
// estimate also has its ID, the set it was made in and when, and whether it is its work order's current estimate.
type Estimate struct {
	ID               string     `json:"id,omitempty"`
	SetID            string     `json:"estimate_set_id,omitempty"`
	WorkOrder        string     `json:"work_order_id,omitempty"`
	Status           Status     `json:"status"`
	Confidence       Confidence `json:"confidence,omitempty"`
	Tier             int        `json:"tier,omitempty"`
	Keys             []string   `json:"keys"`
	DroppedKeys      []string   `json:"dropped_keys"`
	SampleCount      int        `json:"sample_count"`
	MinSamples       int64      `json:"min_samples"`
	SampleIDs        []string   `json:"sample_ids"`
	PoolSamples      int        `json:"pool_samples"`
	DeliveryP50Ms    int64      `json:"delivery_p50_ms"`
	DeliveryP85Ms    int64      `json:"delivery_p85_ms"`
	DeliveryMinMs    int64      `json:"delivery_min_ms"`
	DeliveryMaxMs    int64      `json:"delivery_max_ms"`
	CostP50USD       float64    `json:"cost_p50_usd"`
	CostP85USD       float64    `json:"cost_p85_usd"`
	CostMinUSD       float64    `json:"cost_min_usd"`
	CostMaxUSD       float64    `json:"cost_max_usd"`
	P85Open          bool       `json:"p85_open"`
	CostAtLeast      bool       `json:"cost_at_least"`
	Rev              int64      `json:"rev"`
	EstimatorVersion int        `json:"estimator_version"`
	PolicyVersion    int64      `json:"policy_version"`
	Explanation      string     `json:"explanation"`
	Current          bool       `json:"current"`
	CreatedAt        int64      `json:"created_at,omitempty"`
}

// Time is the estimate's delivery time band.
func (e Estimate) Time() Band { return Band{P50: e.DeliveryP50Ms, P85: e.DeliveryP85Ms} }

// Cost is the estimate's cost band.
func (e Estimate) Cost() CostBand { return CostBand{P50: e.CostP50USD, P85: e.CostP85USD} }

// EstimateInput is what one estimate is made from: the keys of the work order being estimated, which are known
// before it starts; the project's delivery records; the records of the installation's pool from other
// projects, read with the project's own at tier 6 only when the policy shares history; the policy, which names
// its version; and the revision of the ledger the records were read at. A pool record holds kind, size,
// capability class, the durations, the cost and whether it was a lower bound, the outcome, and when it ended, and
// names no work order, repository, or title.
type EstimateInput struct {
	WorkOrder string
	Keys      Keys
	Records   []Record
	Pool      []Record
	Policy    EstimatePolicy
	Rev       int64
}

// EstimateOf estimates one `change` or `rework` work order from comparable finished work (ADR-0026), under the
// policy as the estimator applies it, and with a size left out read as unknown, as delivery records keep it. It reads
// the tiers narrowest first, each from the policy's most recent comparable records that ended `done`. The
// first tier with at least min_samples gives a confident band at the 50th and 85th percentiles by nearest
// rank. Below that at every tier, the tier with the most records, the narrowest among equals, gives a low
// confidence band at the records of rank ceil(0.5 × (n + 1)) and ceil(0.85 × (n + 1)); a p85 rank past n leaves
// the band open above the longest record. With no record at any tier there is no number. The estimate
// explains itself in plain words from its own fields.
func EstimateOf(in EstimateInput) Estimate {
	in.Policy = in.Policy.effective()
	if in.Keys.Size == "" {
		in.Keys.Size = measures.SizeUnknown
	}
	out := Estimate{
		WorkOrder: in.WorkOrder, Status: NoHistory, Keys: []string{}, DroppedKeys: []string{}, SampleIDs: []string{}, MinSamples: in.Policy.MinSamples,
		Rev: in.Rev, EstimatorVersion: EstimatorVersion, PolicyVersion: in.Policy.Version,
	}
	candidates := make([][]Record, 0, len(tiers)+1)
	for _, keys := range tiers {
		candidates = append(candidates, comparable(in.Records, in.Keys, keys, in.Policy.RecentSamples))
	}
	if in.Policy.ShareHistory {
		candidates = append(candidates, comparable(slices.Concat(in.Records, in.Pool), in.Keys, nil, in.Policy.RecentSamples))
	}
	chosen := -1
	for i, records := range candidates {
		if int64(len(records)) >= in.Policy.MinSamples {
			chosen, out.Status, out.Confidence = i, Estimated, Confident
			break
		}
	}
	if chosen < 0 {
		for i, records := range candidates {
			if len(records) > 0 && (chosen < 0 || len(records) > len(candidates[chosen])) {
				chosen, out.Status, out.Confidence = i, Estimated, Low
			}
		}
	}
	if chosen < 0 {
		out.Explanation = explain(out, in.Keys, in.Policy)
		return out
	}
	records := candidates[chosen]
	out.Tier, out.SampleCount = chosen+1, len(records)
	pooled := out.Tier == poolTier
	matched := []string{}
	if !pooled {
		matched = tiers[chosen]
	}
	out.Keys = append([]string{KeyKind, KeySize}, matched...)
	for _, key := range tiers[0] {
		if !slices.Contains(matched, key) {
			out.DroppedKeys = append(out.DroppedKeys, key)
		}
	}
	times := make([]int64, 0, len(records))
	costs := make([]float64, 0, len(records))
	for _, r := range records {
		if r.WorkOrder == "" {
			out.PoolSamples++
		} else {
			out.SampleIDs = append(out.SampleIDs, r.WorkOrder)
		}
		times = append(times, r.DeliveryMs)
		costs = append(costs, r.Usage.CostUSD)
		out.CostAtLeast = out.CostAtLeast || r.Usage.CostLowerBound
	}
	slices.Sort(times)
	slices.Sort(costs)
	out.DeliveryMinMs, out.DeliveryMaxMs = times[0], times[len(times)-1]
	out.CostMinUSD, out.CostMaxUSD = costs[0], costs[len(costs)-1]
	p50, p85 := rank(len(times), 0.5), rank(len(times), 0.85)
	if out.Confidence == Low {
		p50, out.P85Open = lowRank(len(times), 0.5), lowRank(len(times), 0.85) >= len(times)
		p85 = min(lowRank(len(times), 0.85), len(times)-1)
	}
	out.DeliveryP50Ms, out.DeliveryP85Ms = times[p50], times[p85]
	out.CostP50USD, out.CostP85USD = costs[p50], costs[p85]
	out.Explanation = explain(out, in.Keys, in.Policy)
	return out
}

// comparable is the most recent records that ended `done` with the same kind and size and the same values of
// some more keys, newest by end first. Records that ended together order by everything else they hold, so the
// draw never depends on the order the records came in, though a pool record names no work order.
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
	slices.SortFunc(out, func(a, b Record) int {
		return cmp.Or(cmp.Compare(b.Ended, a.Ended), cmp.Compare(a.WorkOrder, b.WorkOrder), cmp.Compare(a.DeliveryMs, b.DeliveryMs),
			cmp.Compare(a.Usage.CostUSD, b.Usage.CostUSD), compareBool(a.Usage.CostLowerBound, b.Usage.CostLowerBound),
			cmp.Compare(a.CapabilityClass, b.CapabilityClass), cmp.Compare(a.Repository, b.Repository), cmp.Compare(a.MergeAuthority, b.MergeAuthority),
			cmp.Compare(a.Criteria, b.Criteria), cmp.Compare(a.ElapsedMs, b.ElapsedMs), cmp.Compare(a.LaneMs, b.LaneMs), cmp.Compare(a.PersonMs, b.PersonMs))
	})
	return out[:min(len(out), int(max(recent, 0)))]
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
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
	if e.Status == NoHistory {
		text := "No estimate: no " + work(1) + " has finished in this project yet"
		if policy.ShareHistory {
			return text + " or in the installation pool."
		}
		return text + ", and this project does not share history with other projects."
	}
	pooled := e.Tier == poolTier
	if e.Confidence == Confident {
		text := fmt.Sprintf("Based on %d finished %s", e.SampleCount, work(e.SampleCount))
		var words []string
		for _, key := range e.Keys {
			if key != KeyKind && key != KeySize {
				words = append(words, keyWords[key])
			}
		}
		switch {
		case pooled:
			return text + " in the installation pool."
		case len(words) > 0:
			return text + " with the same " + list(words) + "."
		}
		return text + "."
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Low confidence. Based on %d finished %s; a confident estimate needs %d.", e.SampleCount, work(e.SampleCount), e.MinSamples)
	if len(e.DroppedKeys) > 0 || pooled {
		words := make([]string, 0, len(e.DroppedKeys))
		for _, key := range e.DroppedKeys {
			words = append(words, "the "+keyWords[key])
		}
		fmt.Fprintf(&out, " To find them, ropeshift ignored %s", list(words))
		if pooled {
			out.WriteString(", and drew on the installation pool")
		}
		out.WriteString(".")
	}
	switch {
	case e.SampleCount == 1:
		fmt.Fprintf(&out, " It took %s.", durationWords(e.DeliveryMinMs))
	case e.DeliveryMinMs == e.DeliveryMaxMs:
		fmt.Fprintf(&out, " They each took %s.", durationWords(e.DeliveryMinMs))
	default:
		fmt.Fprintf(&out, " They took from %s to %s.", durationWords(e.DeliveryMinMs), durationWords(e.DeliveryMaxMs))
	}
	switch {
	case e.P85Open && e.SampleCount == 1:
		fmt.Fprintf(&out, " With one outcome, the 85th percentile cannot be read from it, so the band is open above %s.", durationWords(e.DeliveryMaxMs))
	case e.P85Open:
		fmt.Fprintf(&out, " With fewer than %d, the 85th percentile cannot be read from them, so the band is open above %s.", openBelow, durationWords(e.DeliveryMaxMs))
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

// durationWords says a duration as a person reads it: hours and minutes from an hour on, minutes and seconds
// from a minute on, and seconds below. It rounds to the second, and from an hour on to the minute, before it
// splits into units, so a rounded unit carries into the next, and durations a minute apart read apart.
func durationWords(ms int64) string {
	seconds := (ms + 500) / 1000
	switch {
	case seconds >= 3600:
		minutes := (seconds + 30) / 60
		if minutes%60 == 0 {
			return strconv.FormatInt(minutes/60, 10) + "h"
		}
		return strconv.FormatInt(minutes/60, 10) + "h" + strconv.FormatInt(minutes%60, 10) + "m"
	case seconds >= 60:
		if seconds%60 == 0 {
			return strconv.FormatInt(seconds/60, 10) + "m"
		}
		return strconv.FormatInt(seconds/60, 10) + "m" + strconv.FormatInt(seconds%60, 10) + "s"
	}
	return strconv.FormatInt(seconds, 10) + "s"
}
