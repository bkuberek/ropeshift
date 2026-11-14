package delivery

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// ForecastWork is one unfinished work order a forecast runs (FR-EST-8): the milestone it belongs to, its
// priority, and the work it waits on; its estimate and the records that estimate rests on, which Drawn gives;
// and, once it has started, the delivery time, lane time, and cost it has had so far.
type ForecastWork struct {
	WorkOrder   string
	Title       string
	Milestone   string
	Priority    int64
	DependsOn   []string
	Estimate    Estimate
	Records     []Record
	Started     bool
	SoFarMs     int64
	LaneSoFarMs int64
	SpentUSD    float64
}

// ForecastInput is what one plan version's forecast is made from: the plan, its version's digest and the ledger
// revision, which make the seed; the time it runs from; the lane cap, where zero or less means nothing can run;
// the estimate policy, for its trials, its minimum sample, and its version; the median wait for a person among
// the project's recent delivery records, for the what-if that cuts waits to it; every milestone of the plan
// version, so one whose work all ended has a forecast that says so; the unfinished work outside the plan that its
// work waits on, which the trials do not run; and the plan's unfinished work. A dependency on work that is in
// neither list has ended. Now and the work's time so far must be read at the revision, so the same revision and
// plan version give the same forecast.
type ForecastInput struct {
	Plan           string
	PlanDigest     string
	Rev            int64
	Now            int64
	Cap            int64
	Policy         EstimatePolicy
	PersonMedianMs int64
	Milestones     []string
	Outside        []string
	Work           []ForecastWork
}

// ForecastStatus says whether a forecast has dates: `forecast`; `no_history`, when no unfinished work order in it
// has finished history; `no_capacity`, when the project has no lane to run its work; or `finished`, when no work
// order in it is unfinished.
type ForecastStatus string

// The forecast statuses.
const (
	Forecasted         ForecastStatus = "forecast"
	ForecastNoHistory  ForecastStatus = "no_history"
	ForecastNoCapacity ForecastStatus = "no_capacity"
	ForecastFinished   ForecastStatus = "finished"
)

// LowerBound says which of a forecast's results have no upper bound: `all` when work with no history is left
// out of the trials, when started work has run longer than every comparable record, or when the plan's work waits
// on unfinished work outside it; `p85` when an input's draws are open above their longest record; and `none`
// otherwise. A lower-bound date reads "or later", and a lower-bound cost "at least".
type LowerBound string

// The lower bounds.
const (
	LowerNone LowerBound = "none"
	LowerP85  LowerBound = "p85"
	LowerAll  LowerBound = "all"
)

// Forecast is one milestone's or the plan's forecast (FR-EST-8, FR-EST-9, FR-EST-14), with what makes it again:
// the seed, the trials, the cap, the time it ran from, the median wait, the estimate policy version, and the
// revision. End dates are epoch milliseconds, and costs are estimated at list price. Milestone is empty for the
// plan's own. LowConfidenceWorkOrders names the unfinished work its labels rest on whose estimate is low or has
// no history, or that has run longer than most of what it draws on; LeftOut those of them left out of the trials;
// and Outside the unfinished work outside the plan it waits on. Explanation is the text every surface shows.
type Forecast struct {
	Plan                    string         `json:"plan_id"`
	Milestone               string         `json:"milestone_id,omitempty"`
	Status                  ForecastStatus `json:"status"`
	Rev                     int64          `json:"ledger_rev"`
	Seed                    string         `json:"seed"`
	Trials                  int64          `json:"trials"`
	Cap                     int64          `json:"cap"`
	AsOf                    int64          `json:"as_of"`
	PersonMedianMs          int64          `json:"person_median_ms"`
	PolicyVersion           int64          `json:"policy_version"`
	EndP50                  int64          `json:"end_p50"`
	EndP85                  int64          `json:"end_p85"`
	CostP50USD              float64        `json:"cost_p50_usd"`
	CostP85USD              float64        `json:"cost_p85_usd"`
	EndP85CapPlusOne        int64          `json:"end_p85_cap_plus_one"`
	EndP85PersonMedian      int64          `json:"end_p85_person_median"`
	Confidence              Confidence     `json:"confidence,omitempty"`
	LowConfidenceWorkOrders []string       `json:"low_confidence_work_orders"`
	LeftOut                 []string       `json:"left_out"`
	Outside                 []string       `json:"outside"`
	LowerBound              LowerBound     `json:"lower_bound,omitempty"`
	Explanation             string         `json:"explanation"`
}

// ForecastSeed is the seed of a plan version's forecast at a ledger revision: the digest of the version's digest,
// the revision, and the estimator version, so the same ledger revision and plan version give the same forecast.
func ForecastSeed(planDigest string, rev int64) string {
	sum := sha256.Sum256([]byte(planDigest + "\x00" + strconv.FormatInt(rev, 10) + "\x00" + strconv.Itoa(EstimatorVersion)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// draws is the forecast's source of chance: SplitMix64 from the seed, written out here so the same seed draws the
// same records under every build.
type draws struct{ state uint64 }

func drawsFrom(seed string) *draws {
	sum := sha256.Sum256([]byte(seed))
	// Sixteen hex digits always read as a uint64.
	state, _ := strconv.ParseUint(hex.EncodeToString(sum[:8]), 16, 64)
	return &draws{state: state}
}

func (d *draws) next() uint64 {
	d.state += 0x9e3779b97f4a7c15
	z := d.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// index draws one of n, each as likely as the others up to a bias below n in 2^64.
func (d *draws) index(n int) int { return int(d.next() % uint64(n)) }

// job is one work order as a trial runs it: how long it holds a lane, how long it takes in all, and what it
// costs; work left out takes no time, no lane, and no cost.
type job struct {
	lane, total int64
	cost        float64
	leftOut     bool
}

// forecastGroup is the work one forecast covers: the plan's, or a milestone's, whose end and cost it reads.
type forecastGroup struct {
	milestone string
	own       []int
	ends      [3][]int64
	costs     []float64
}

// The three runs of each trial: as it stands, with one more lane, and with waits for a person cut to the median.
const (
	runAsIs = iota
	runCapPlusOne
	runPersonMedian
)

// footing is what one work order's draws rest on, for a forecast's labels: how many records, whether they are
// fewer than a confident estimate needs, whether they are open above the longest, and, for started work, whether
// it ran longer than every one.
type footing struct {
	count   int
	low     bool
	open    bool
	overrun bool
}

// ForecastOf forecasts a plan version's milestones and the plan with a seeded simulation (FR-EST-8, FR-EST-9).
// Each trial draws one record at random for each unfinished work order with history, from the records its
// estimate rests on; started work draws only from records that ran longer than it has so far and keeps the
// remainder, or, with none longer, any record and all of it. Work with no history takes no time, no lane, and no
// cost, so its dependents wait only for its own dependencies, and so does work outside the plan. The trial runs
// the work in dependency order, by priority within what is ready, at most the cap at a time: each work order
// holds a lane for its lane time and then needs the rest of its delivery time without one. The same draws run
// again with the cap one higher, and with each record's waits for a person cut to the median, for the two
// what-ifs. The p50 and p85 come from the trials by nearest rank. A work order listed twice counts once, as
// first listed, and a dependency on itself counts for nothing. It returns the plan's forecast, then each
// milestone's, by milestone.
func ForecastOf(in ForecastInput) []Forecast {
	seed := ForecastSeed(in.PlanDigest, in.Rev)
	trials := min(max(in.Policy.ForecastTrials, 1), MaxForecastTrials)
	var work []ForecastWork
	listed := map[string]bool{}
	for _, w := range in.Work {
		if !listed[w.WorkOrder] {
			listed[w.WorkOrder] = true
			work = append(work, w)
		}
	}
	slices.SortStableFunc(work, func(a, b ForecastWork) int { return cmp.Compare(a.WorkOrder, b.WorkOrder) })
	at := map[string]int{}
	for i, w := range work {
		at[w.WorkOrder] = i
	}
	deps := make([][]int, len(work))
	for i, w := range work {
		for _, id := range w.DependsOn {
			if j, held := at[id]; held && j != i && !slices.Contains(deps[i], j) {
				deps[i] = append(deps[i], j)
			}
		}
		slices.Sort(deps[i])
	}
	groups := forecastGroups(work, in.Milestones)
	if in.Cap > 0 {
		order := runOrder(work)
		rng := drawsFrom(seed)
		asIs, cut := make([]job, len(work)), make([]job, len(work))
		for range trials {
			for i, w := range work {
				asIs[i], cut[i] = drawJob(w, rng, in.PersonMedianMs)
			}
			runs := [3][]int64{simulate(asIs, deps, order, in.Cap), simulate(asIs, deps, order, in.Cap+1), simulate(cut, deps, order, in.Cap)}
			for g := range groups {
				for run, ends := range runs {
					var end int64
					for _, i := range groups[g].own {
						end = max(end, ends[i])
					}
					groups[g].ends[run] = append(groups[g].ends[run], end)
				}
				var cost float64
				for _, i := range groups[g].own {
					cost += asIs[i].cost
				}
				groups[g].costs = append(groups[g].costs, cost)
			}
		}
	}
	labels := labelsOf(in, work)
	out := make([]Forecast, 0, len(groups))
	for _, g := range groups {
		out = append(out, forecastOfGroup(in, work, g, labels, seed, trials))
	}
	return out
}

// forecastGroups lists the plan's group and then each milestone's, by milestone: every milestone the plan version
// names, and every one its unfinished work belongs to.
func forecastGroups(work []ForecastWork, milestones []string) []forecastGroup {
	plan := forecastGroup{}
	byMilestone := map[string][]int{}
	for _, milestone := range milestones {
		if milestone != "" {
			byMilestone[milestone] = nil
		}
	}
	for i, w := range work {
		plan.own = append(plan.own, i)
		if w.Milestone != "" {
			byMilestone[w.Milestone] = append(byMilestone[w.Milestone], i)
		}
	}
	groups := []forecastGroup{plan}
	names := make([]string, 0, len(byMilestone))
	for milestone := range byMilestone {
		names = append(names, milestone)
	}
	slices.Sort(names)
	for _, milestone := range names {
		groups = append(groups, forecastGroup{milestone: milestone, own: byMilestone[milestone]})
	}
	return groups
}

// runOrder is the order ready work takes a lane: started work first, then by priority, highest first, as dispatch
// offers it, then by work order.
func runOrder(work []ForecastWork) []int {
	out := make([]int, len(work))
	for i := range out {
		out[i] = i
	}
	slices.SortStableFunc(out, func(a, b int) int {
		return cmp.Or(compareBool(work[b].Started, work[a].Started), cmp.Compare(work[b].Priority, work[a].Priority), cmp.Compare(a, b))
	})
	return out
}

// leftOut says a work order takes no part in the trials: its estimate has no history.
func leftOut(w ForecastWork) bool { return w.Estimate.Status != Estimated || len(w.Records) == 0 }

// longerThanSoFar is the records a started work order draws from: those that ran longer than it has so far.
func longerThanSoFar(w ForecastWork) []Record {
	var out []Record
	for _, r := range w.Records {
		if r.DeliveryMs > w.SoFarMs {
			out = append(out, r)
		}
	}
	return out
}

// footingOf reads what a work order's draws rest on. Work not started rests on its estimate. Started work rests
// on the records that ran longer than it has so far, which are low below the minimum sample and open below six,
// as an estimate's are; with none, it has overrun them all.
func footingOf(w ForecastWork, minSamples int64) footing {
	if !w.Started {
		return footing{count: w.Estimate.SampleCount, low: w.Estimate.Confidence == Low, open: w.Estimate.P85Open}
	}
	longer := len(longerThanSoFar(w))
	switch {
	case longer == 0:
		return footing{low: true, overrun: true}
	case longer == len(w.Records):
		return footing{count: w.Estimate.SampleCount, low: w.Estimate.Confidence == Low, open: w.Estimate.P85Open}
	}
	low := w.Estimate.Confidence == Low || int64(longer) < minSamples
	return footing{count: longer, low: low, open: low && longer < openBelow}
}

// drawJob draws one record for a work order and gives the job as it stands and with waits for a person cut to the
// median. Work left out draws nothing. Started work is not cut, because a record does not say which of its waits
// lie in the time the work has already had.
func drawJob(w ForecastWork, rng *draws, personMedianMs int64) (job, job) {
	if leftOut(w) {
		return job{leftOut: true}, job{leftOut: true}
	}
	from, soFar, laneSoFar, spent := w.Records, int64(0), int64(0), 0.0
	if w.Started {
		if longer := longerThanSoFar(w); len(longer) > 0 {
			from, soFar, laneSoFar, spent = longer, w.SoFarMs, w.LaneSoFarMs, w.SpentUSD
		}
	}
	r := from[rng.index(len(from))]
	jobOf := func(delivery int64) job {
		total := max(delivery-soFar, 0)
		return job{lane: min(max(r.LaneMs-laneSoFar, 0), total), total: total, cost: max(r.Usage.CostUSD-spent, 0)}
	}
	asIs := jobOf(r.DeliveryMs)
	if w.Started || personMedianMs <= 0 {
		return asIs, asIs
	}
	var cutBy int64
	for _, wait := range r.PersonWaits {
		cutBy += max(wait-personMedianMs, 0)
	}
	return asIs, jobOf(r.DeliveryMs - cutBy)
}

// simulate runs one trial's jobs and returns when each ends, from the start of the trial. Work becomes ready when
// every work order it depends on ended; ready work takes a free lane in run order, and work that needs no lane
// starts at once. A dependency that can never end, which a cycle would be, ends with the last of the rest.
func simulate(jobs []job, deps [][]int, order []int, lanes int64) []int64 {
	n := len(jobs)
	end := make([]int64, n)
	startedAt := make([]int64, n)
	state := make([]int8, n) // 0 waiting, 1 ready, 2 running, 3 ended
	var held []int64         // when each lane in use comes free
	var t int64
	ended := 0
	for ended < n {
		for changed := true; changed; {
			changed = false
			kept := held[:0]
			for _, free := range held {
				if free > t {
					kept = append(kept, free)
				}
			}
			held = kept
			for _, i := range order {
				switch {
				case state[i] == 2 && startedAt[i]+jobs[i].total <= t:
					state[i], end[i], changed = 3, t, true
					ended++
				case state[i] == 0 && dependenciesEnded(i, deps, state):
					state[i], changed = 1, true
				}
			}
			for _, i := range order {
				if state[i] != 1 {
					continue
				}
				switch {
				case jobs[i].leftOut:
					state[i], end[i], changed = 3, t, true
					ended++
				case jobs[i].lane == 0:
					state[i], startedAt[i], changed = 2, t, true
				case int64(len(held)) < lanes:
					held = append(held, t+jobs[i].lane)
					state[i], startedAt[i], changed = 2, t, true
				}
			}
		}
		if ended == n {
			break
		}
		next := int64(math.MaxInt64)
		for _, free := range held {
			next = min(next, free)
		}
		for i := range jobs {
			if state[i] == 2 {
				next = min(next, startedAt[i]+jobs[i].total)
			}
		}
		if next == math.MaxInt64 {
			for i := range jobs {
				if state[i] != 3 {
					state[i], end[i] = 3, t
				}
			}
			break
		}
		t = next
	}
	return end
}

func dependenciesEnded(i int, deps [][]int, state []int8) bool {
	for _, j := range deps[i] {
		if state[j] != 3 {
			return false
		}
	}
	return true
}

// percentile is the value of nearest rank p among values, which it sorts.
func percentile[T cmp.Ordered](values []T, p float64) T {
	slices.Sort(values)
	return values[rank(len(values), p)]
}

// forecastLabels are the labels every forecast of a plan version shares. Under a lane cap any unfinished work
// order can take a lane before a milestone's own work, so each milestone's dates rest on all of the plan's work,
// as the plan's do: the work orders that make them low confidence, those left out, the work outside the plan
// they wait on, the lower bound, and the sentences that say why.
type forecastLabels struct {
	simulated  int
	lowWork    []string
	leftOut    []string
	outside    []string
	open       bool
	overrun    bool
	reasons    []string
	notes      []string
	lowerBound LowerBound
}

// labelsOf reads the plan's labels from its work.
func labelsOf(in ForecastInput, work []ForecastWork) forecastLabels {
	minSamples := in.Policy.effective().MinSamples
	l := forecastLabels{lowWork: []string{}, leftOut: []string{}, outside: []string{}}
	waitsOutside := map[string]bool{}
	started, pooledUncut := false, false
	for _, w := range work {
		for _, id := range w.DependsOn {
			if slices.Contains(in.Outside, id) {
				waitsOutside[id] = true
			}
		}
		if leftOut(w) {
			l.lowWork, l.leftOut = append(l.lowWork, w.WorkOrder), append(l.leftOut, w.WorkOrder)
			l.reasons = append(l.reasons, nameOf(w)+" has no finished history and is left out, so the dates and costs are lower bounds.")
			continue
		}
		l.simulated++
		started = started || w.Started
		for _, r := range w.Records {
			pooledUncut = pooledUncut || (r.WorkOrder == "" && r.PersonMs > 0 && len(r.PersonWaits) == 0)
		}
		f := footingOf(w, minSamples)
		if f.low {
			l.lowWork = append(l.lowWork, w.WorkOrder)
		}
		switch {
		case f.overrun:
			l.overrun = true
			l.reasons = append(l.reasons, fmt.Sprintf("%s has run %s so far, longer than every comparable record, so its remaining time is a guess and the dates are lower bounds.",
				nameOf(w), durationWords(w.SoFarMs)))
		case f.low && w.Started && f.count < len(w.Records):
			l.reasons = append(l.reasons, fmt.Sprintf("%s has run %s so far, and %d comparable %s longer; a confident estimate needs %d.",
				nameOf(w), durationWords(w.SoFarMs), f.count, plural(f.count, "record ran", "records ran"), minSamples))
		case f.low:
			l.reasons = append(l.reasons, fmt.Sprintf("%s rests on %d finished %s, and a confident estimate needs %d.",
				nameOf(w), f.count, plural(f.count, "record", "records"), minSamples))
		}
		if f.open {
			l.open = true
			l.reasons = append(l.reasons, nameOf(w)+"'s band is open above its longest record, so the 85th percentile is a lower bound.")
		}
	}
	for id := range waitsOutside {
		l.outside = append(l.outside, id)
	}
	slices.Sort(l.outside)
	if len(l.outside) > 0 {
		l.notes = append(l.notes, "Its work waits on unfinished work outside this plan ("+strings.Join(l.outside, ", ")+"), which the trials do not run, so the dates are lower bounds.")
	}
	if started {
		l.notes = append(l.notes, "The what-if that cuts waits for a person to the median leaves started work as it is, because a record does not say which of its waits are past.")
	}
	if pooledUncut {
		l.notes = append(l.notes, "That what-if cannot cut the waits of records from the installation pool, which keeps no waits.")
	}
	l.lowerBound = LowerNone
	switch {
	case len(l.leftOut) > 0 || l.overrun || len(l.outside) > 0:
		l.lowerBound = LowerAll
	case l.open:
		l.lowerBound = LowerP85
	}
	return l
}

// forecastOfGroup reads one group's trials into its forecast, with the plan's labels and explanation.
func forecastOfGroup(in ForecastInput, work []ForecastWork, g forecastGroup, l forecastLabels, seed string, trials int64) Forecast {
	f := Forecast{
		Plan: in.Plan, Milestone: g.milestone, Rev: in.Rev, Seed: seed, Trials: trials, Cap: in.Cap, AsOf: in.Now,
		PersonMedianMs: in.PersonMedianMs, PolicyVersion: in.Policy.Version, LowConfidenceWorkOrders: l.lowWork, LeftOut: l.leftOut, Outside: l.outside,
	}
	whose := "plan"
	if g.milestone != "" {
		whose = "milestone"
	}
	withHistory := 0
	for _, i := range g.own {
		if !leftOut(work[i]) {
			withHistory++
		}
	}
	switch {
	case len(g.own) == 0:
		f.Status, f.Explanation = ForecastFinished, "Nothing is left to forecast: every work order of this "+whose+" has ended."
		return f
	case in.Cap <= 0:
		f.Status, f.Explanation = ForecastNoCapacity, "No forecast: the project has no lane to run its work, because no worker serves it."
		return f
	case withHistory == 0:
		f.Status = ForecastNoHistory
		f.Explanation = fmt.Sprintf("No forecast: none of the %d unfinished %s of this %s has finished history.",
			len(g.own), plural(len(g.own), "work order", "work orders"), whose)
		return f
	}
	f.Status, f.Confidence, f.LowerBound = Forecasted, Confident, l.lowerBound
	if len(l.lowWork) > 0 || l.open {
		f.Confidence = Low
	}
	f.EndP50, f.EndP85 = in.Now+percentile(g.ends[runAsIs], 0.5), in.Now+percentile(g.ends[runAsIs], 0.85)
	f.EndP85CapPlusOne = in.Now + percentile(g.ends[runCapPlusOne], 0.85)
	f.EndP85PersonMedian = in.Now + percentile(g.ends[runPersonMedian], 0.85)
	f.CostP50USD, f.CostP85USD = percentile(g.costs, 0.5), percentile(g.costs, 0.85)
	basis := fmt.Sprintf("%d trials of %d %s, at most %d at a time", trials, l.simulated, plural(l.simulated, "work order", "work orders"), in.Cap)
	sentences := []string{"Based on " + basis + "."}
	if f.Confidence == Low {
		sentences = append([]string{"Low confidence, from " + basis + "."}, l.reasons...)
	}
	f.Explanation = strings.Join(append(sentences, l.notes...), " ")
	return f
}

// nameOf names a work order as a person reads it: its title and ID, or its ID alone.
func nameOf(w ForecastWork) string {
	if w.Title == "" {
		return w.WorkOrder
	}
	return w.Title + " (" + w.WorkOrder + ")"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
