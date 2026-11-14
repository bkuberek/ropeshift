package delivery

import (
	"cmp"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
)

// Span is a stretch of time, from From up to To.
type Span struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// Union joins every span of the sets that overlap or touch, in order, and drops the empty ones.
func Union(sets ...[]Span) []Span {
	var all []Span
	for _, set := range sets {
		for _, s := range set {
			if s.To > s.From {
				all = append(all, s)
			}
		}
	}
	slices.SortFunc(all, func(a, b Span) int { return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To)) })
	var out []Span
	for _, s := range all {
		if n := len(out); n > 0 && s.From <= out[n-1].To {
			out[n-1].To = max(out[n-1].To, s.To)
			continue
		}
		out = append(out, s)
	}
	return out
}

// Intersect keeps the time every set holds. With no set, nothing is held.
func Intersect(sets ...[]Span) []Span {
	if len(sets) == 0 {
		return nil
	}
	out := Union(sets[0])
	for _, set := range sets[1:] {
		other := Union(set)
		var next []Span
		for i, j := 0, 0; i < len(out) && j < len(other); {
			if from, to := max(out[i].From, other[j].From), min(out[i].To, other[j].To); from < to {
				next = append(next, Span{From: from, To: to})
			}
			if out[i].To < other[j].To {
				i++
			} else {
				j++
			}
		}
		out = next
	}
	return out
}

// Full reads capacity intervals as the spans in which each worker stood at one of its lane caps, by worker:
// either cap, each to its end, or to its last report while it stands (FR-OBS-14). A project's lanes were
// capped while every worker serving it was full, which Intersect gives from these.
func Full(intervals []ledger.CapacityInterval) map[string][]Span {
	by := map[string][]Span{}
	for _, c := range intervals {
		by[c.Worker] = append(by[c.Worker], Span{From: c.StartedAt, To: c.End()})
	}
	for worker, spans := range by {
		by[worker] = Union(spans)
	}
	return by
}
