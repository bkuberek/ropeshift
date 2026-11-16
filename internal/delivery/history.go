package delivery

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/workorders"
)

// ChangeMedian is the median change size of the work in a group whose lane recorded one: files changed, lines
// added, and lines removed, each by nearest rank, over the Measured records that hold a change size.
type ChangeMedian struct {
	Measured int64 `json:"measured"`
	Files    int64 `json:"files"`
	Added    int64 `json:"added"`
	Removed  int64 `json:"removed"`
}

// HistoryGroup is what a planning lane reads of one group of its project's finished work (FR-EST-12): how many
// work orders it holds, and their delivery time, lane time, and estimated cost at p50 and p85 by nearest rank, the
// mean rounds, and the median change size, with the confidence label and explanation an estimate from just these
// records carries. A group under the minimum reads its percentiles at the estimator's own ranks, ceil(0.5 x (n + 1))
// and ceil(0.85 x (n + 1)), and P85Open says its p85 is the longest and cannot be read from so few, so the figures say
// what the explanation says. CapabilityClass and Repository are empty in a group of kind and size alone. CostLowerBound says
// some cost was never reported, and Change is absent when no work order of the group recorded a change size.
type HistoryGroup struct {
	Kind            string        `json:"kind"`
	Size            string        `json:"size"`
	CapabilityClass string        `json:"capability_class,omitempty"`
	Repository      string        `json:"repository,omitempty"`
	Count           int64         `json:"count"`
	Delivery        Quantiles     `json:"delivery"`
	Lane            Quantiles     `json:"lane"`
	Cost            CostQuantiles `json:"cost"`
	CostLowerBound  bool          `json:"cost_lower_bound,omitempty"`
	P85Open         bool          `json:"p85_open,omitempty"`
	MeanRounds      float64       `json:"mean_rounds"`
	Change          *ChangeMedian `json:"change,omitempty"`
	Confidence      Confidence    `json:"confidence"`
	Explanation     string        `json:"explanation"`
}

// History is a project's finished work as a planning lane reads it, in two groupings: by kind and size, and by
// kind, size, capability class, and repository. Each group reads its RecentSamples most recent work orders that
// ended `done`, as an estimate does, and is low confidence under MinSamples.
type History struct {
	MinSamples                int64          `json:"min_samples"`
	RecentSamples             int64          `json:"recent_samples"`
	ByKindSize                []HistoryGroup `json:"by_kind_size"`
	ByKindSizeClassRepository []HistoryGroup `json:"by_kind_size_class_repository"`
}

// historyKey names one group of either grouping, where the keys of the narrower one are empty in the other.
type historyKey struct{ kind, size, class, repository string }

// historyAuthority is the merge authority level a group's estimate asks for, which no record holds. Every tier above
// the last matches on merge authority, so asking for a level that does not exist leaves a group only the last tier
// of kind and size, over exactly the records it holds.
const historyAuthority = "history"

// HistoryOf reads a project's delivery records as a planning lane's history (FR-EST-12). It reads the project's
// own records alone, whatever the policy says of sharing: the installation pool answers only the last tier of an
// estimate (FR-EST-13), and a figure drawn across projects would show another project's times. A group's label and
// words come from the estimator over its own records, under the policy as the estimator applies it, with the keys
// the grouping matched named as matched and the others as set aside.
func HistoryOf(records []Record, policy EstimatePolicy) History {
	policy = policy.effective()
	return History{
		MinSamples: policy.MinSamples, RecentSamples: policy.RecentSamples,
		ByKindSize:                groupsOfHistory(records, policy, false),
		ByKindSizeClassRepository: groupsOfHistory(records, policy, true),
	}
}

func groupsOfHistory(records []Record, policy EstimatePolicy, narrow bool) []HistoryGroup {
	seen := map[historyKey]bool{}
	var keys []historyKey
	for _, r := range records {
		if r.Outcome != workorders.Done {
			continue
		}
		key := historyKey{kind: r.Kind, size: r.Size}
		if narrow {
			key.class, key.repository = r.CapabilityClass, r.Repository
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b historyKey) int {
		return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.size, b.size), cmp.Compare(a.class, b.class), cmp.Compare(a.repository, b.repository))
	})
	out := make([]HistoryGroup, 0, len(keys))
	for _, key := range keys {
		want := Keys{Kind: key.kind, Size: key.size, CapabilityClass: key.class, Repository: key.repository}
		var matched []string
		if narrow {
			matched = []string{KeyCapabilityClass, KeyRepository}
		}
		out = append(out, groupOfHistory(comparable(records, want, matched, policy.RecentSamples), want, matched, policy))
	}
	return out
}

// groupOfHistory aggregates one group's records, newest first, and labels it.
func groupOfHistory(drawn []Record, want Keys, matched []string, policy EstimatePolicy) HistoryGroup {
	group := HistoryGroup{
		Kind: want.Kind, Size: want.Size, CapabilityClass: want.CapabilityClass, Repository: want.Repository, Count: int64(len(drawn)),
		Delivery: quantiles(drawn, func(r Record) int64 { return r.DeliveryMs }), Lane: quantiles(drawn, func(r Record) int64 { return r.LaneMs }),
	}
	group.Cost, group.CostLowerBound = costQuantiles(drawn)
	var rounds int64
	var files, added, removed []int64
	for _, r := range drawn {
		rounds += r.Rounds
		if r.Change != nil {
			files, added, removed = append(files, r.Change.Files), append(added, r.Change.Added), append(removed, r.Change.Removed)
		}
	}
	group.MeanRounds = float64(rounds) / float64(len(drawn))
	if len(files) > 0 {
		group.Change = &ChangeMedian{Measured: int64(len(files)), Files: median(files), Added: median(added), Removed: median(removed)}
	}
	asked := want
	asked.MergeAuthority = historyAuthority
	e := EstimateOf(EstimateInput{Keys: asked, Records: drawn, Policy: policy, NoPool: true})
	e.Keys = append([]string{KeyKind, KeySize}, matched...)
	e.DroppedKeys = nil
	for _, key := range tiers[0] {
		if !slices.Contains(matched, key) {
			e.DroppedKeys = append(e.DroppedKeys, key)
		}
	}
	if e.Confidence == Low {
		// The estimator's own ranks, so a thin group's figures are the band an estimate from it would give.
		group.Delivery, group.Cost, group.P85Open = Quantiles{P50: e.DeliveryP50Ms, P85: e.DeliveryP85Ms}, CostQuantiles{P50: e.CostP50USD, P85: e.CostP85USD}, e.P85Open
		lane := slices.Sorted(slices.Values(laneTimes(drawn)))
		group.Lane = Quantiles{P50: lane[lowRank(len(lane), 0.5)], P85: lane[min(lowRank(len(lane), 0.85), len(lane)-1)]}
	}
	group.Confidence, group.Explanation = e.Confidence, explain(e, want, policy, true, poolOpening{})
	return group
}

func laneTimes(records []Record) []int64 {
	out := make([]int64, 0, len(records))
	for _, r := range records {
		out = append(out, r.LaneMs)
	}
	return out
}

// median is the 50th percentile of values by nearest rank, of a list that has one.
func median(values []int64) int64 {
	sorted := slices.Sorted(slices.Values(values))
	return sorted[rank(len(sorted), 0.5)]
}

// Text writes the history as a planning lane reads it in its context pack: each group on a line of its own, its
// repository by the name the project gives it where names has one, a low group with the explanation an estimate from
// it would carry, and a note that the figures are history and no promise. A project with no finished work says so.
func (h History) Text(repositories map[string]string) string {
	var out strings.Builder
	out.WriteString("The work this project has finished, from its own delivery records. Each group reads its " +
		fmt.Sprintf("%d most recent work orders that finished `done`; a group under %d is labeled low confidence, with what an estimate from it would say. ", h.RecentSamples, h.MinSamples) +
		"Delivery time leaves out waiting for a lane, lane time is how long a lane held a place, and cost is estimated at list price. " +
		"These figures are history and no promise: use them to size work and choose agents, and do not write an estimate.\n")
	if len(h.ByKindSize) == 0 {
		out.WriteString("\nNo work has finished in this project yet, so it has no history.\n")
		return out.String()
	}
	for _, section := range []struct {
		title  string
		groups []HistoryGroup
	}{{"By kind and size", h.ByKindSize}, {"By kind, size, capability class, and repository", h.ByKindSizeClassRepository}} {
		out.WriteString("\n### " + section.title + "\n\n")
		for _, g := range section.groups {
			out.WriteString(g.line(repositories))
		}
	}
	return out.String()
}

// line is one group as a line of text.
func (g HistoryGroup) line(repositories map[string]string) string {
	label := []string{g.Kind, g.Size}
	if g.CapabilityClass != "" {
		label = append(label, g.CapabilityClass)
	}
	if g.Repository != "" {
		label = append(label, cmp.Or(repositories[g.Repository], g.Repository))
	}
	cost := func(usd float64) string {
		if g.CostLowerBound {
			return fmt.Sprintf("at least $%.2f", usd)
		}
		return fmt.Sprintf("$%.2f", usd)
	}
	// An open band reads as more than its p85, which is its longest record.
	more := ""
	if g.P85Open {
		more = "more than "
	}
	change := "no change size recorded"
	if g.Change != nil {
		change = fmt.Sprintf("median change %d files, +%d -%d lines, of %d measured", g.Change.Files, g.Change.Added, g.Change.Removed, g.Change.Measured)
	}
	said := g.Explanation
	if g.Confidence == Confident {
		said = "Confident. " + said
	}
	return fmt.Sprintf("- %s: %d finished; delivery p50 %s, p85 %s%s; lane time p50 %s, p85 %s%s; cost p50 %s, p85 %s%s; mean %.1f rounds; %s. %s\n",
		strings.Join(label, ", "), g.Count, durationWords(g.Delivery.P50), more, durationWords(g.Delivery.P85), durationWords(g.Lane.P50), more, durationWords(g.Lane.P85),
		cost(g.Cost.P50), more, cost(g.Cost.P85), g.MeanRounds, change, said)
}
