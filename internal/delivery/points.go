package delivery

import (
	"cmp"
	"slices"

	"ropeshift.com/ropeshift/internal/measures"
)

// Points lists look back over a window as the measures tick exports it (FR-OBS-15): elapsed and delivery
// time at p50 and p85 and throughput per week, by kind, size, capability class, and repository, and the share
// of elapsed time in each segment kind, by kind and size. No label names a work order, a title, or a person.
// A group with no work in the window has no point, and a duration with no work done has none.
func Points(records []Record, from, to int64) []measures.Point {
	type key struct{ kind, size, class, repository string }
	byKey := map[key][]Record{}
	type shareKey struct{ kind, size string }
	byShare := map[shareKey][]Record{}
	for _, r := range records {
		byKey[key{r.Kind, r.Size, r.CapabilityClass, r.Repository}] = append(byKey[key{r.Kind, r.Size, r.CapabilityClass, r.Repository}], r)
		byShare[shareKey{r.Kind, r.Size}] = append(byShare[shareKey{r.Kind, r.Size}], r)
	}
	keys := make([]key, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b key) int {
		return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.size, b.size), cmp.Compare(a.class, b.class), cmp.Compare(a.repository, b.repository))
	})
	var out []measures.Point
	for _, k := range keys {
		g := groupOf("", byKey[k], from, to)
		if g.Done+g.Failed+g.Canceled == 0 {
			continue
		}
		labels := func(extra map[string]string) map[string]string {
			out := map[string]string{"kind": k.kind, "size": k.size, "capability_class": k.class, "repository": k.repository}
			for name, value := range extra {
				out[name] = value
			}
			return out
		}
		out = append(out, measures.Point{Name: "delivery.throughput", Unit: "{work_order}", Value: g.PerWeek, Labels: labels(nil)})
		if g.Elapsed == nil {
			continue
		}
		for _, q := range []struct {
			name              string
			elapsed, delivery int64
		}{{"p50", g.Elapsed.P50, g.Delivery.P50}, {"p85", g.Elapsed.P85, g.Delivery.P85}} {
			out = append(out,
				measures.Point{Name: "delivery.elapsed", Unit: "ms", Value: float64(q.elapsed), Labels: labels(map[string]string{"quantile": q.name})},
				measures.Point{Name: "delivery.time", Unit: "ms", Value: float64(q.delivery), Labels: labels(map[string]string{"quantile": q.name})})
		}
	}
	shareKeys := make([]shareKey, 0, len(byShare))
	for k := range byShare {
		shareKeys = append(shareKeys, k)
	}
	slices.SortFunc(shareKeys, func(a, b shareKey) int { return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.size, b.size)) })
	for _, k := range shareKeys {
		g := groupOf("", byShare[k], from, to)
		kinds := make([]string, 0, len(g.Share))
		for kind := range g.Share {
			kinds = append(kinds, string(kind))
		}
		slices.Sort(kinds)
		for _, kind := range kinds {
			out = append(out, measures.Point{
				Name: "delivery.segment_share", Unit: "1", Value: g.Share[Kind(kind)],
				Labels: map[string]string{"kind": k.kind, "size": k.size, "segment_kind": kind},
			})
		}
	}
	return out
}
