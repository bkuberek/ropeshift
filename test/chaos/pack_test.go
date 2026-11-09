//go:build chaos

package chaos

import (
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/contract"
)

// checkClaimsPinned holds every claim to a pack that exists, whichever side of a kill it stands on.
func checkClaimsPinned(w *world) error {
	return contract.ClaimsPinned(w.rig, w.store)
}

// oneWayCase kills a one-way decision on its way to the lanes it covers. It must leave nothing, or
// the decision and everything it told.
func oneWayCase() crashCase {
	return crashCase{
		name:  "a one-way decision",
		op:    "decision.record",
		steps: []string{"noticed"},
		prepare: func(w *world) (childSpec, error) {
			return childSpec{Role: "one-way", Key: w.rig.ID("d")}, nil
		},
		settled: func(w *world, spec childSpec) error {
			return countEvents(w, questions.EventDecisionRecorded, spec.Key, 1)
		},
	}
}

// packVariants are the broken packs the pack checks must catch.
func packVariants() []variant {
	return []variant{
		{name: "a parked lane that stays asked to park", control: control.Sabotage{KeepsParkWanted: true}, check: "N15"},
		{name: "a notice that does not read the index", control: control.Sabotage{IgnoresPins: true}, check: "N3"},
		{
			name:     "a record that pins a pack past the version that held it",
			sabotage: sqlstore.Sabotage{Rewrite: replace("pack.pinning", " AND i.version = (SELECT max(p.version) FROM context_packs p WHERE p.workspace_id = i.workspace_id AND p.id = i.pack_id)", "")}, check: "N19",
		},
		{
			name:     "a new version that loses a decision's replacement",
			sabotage: sqlstore.Sabotage{Rewrite: replace("decision.replaced_by", "AND supersedes = :supersedes", "AND supersedes = :supersedes AND id = ''")}, check: "N4",
		},
		{name: "a pack the log does not hold", control: control.Sabotage{DropsPackEvent: true}, check: "PK3"},
		{name: "a woken lane that takes a new pack", control: control.Sabotage{RebuildsPack: true}, check: "PK4"},
		{
			name:     "a scope read that stops at a page",
			sabotage: sqlstore.Sabotage{Rewrite: replace("decision.overlapping", "ORDER BY d.id", "ORDER BY d.id LIMIT 500")}, check: "PK7",
		},
		{
			name:     "a generation that resumed, read as a fresh baseline",
			sabotage: sqlstore.Sabotage{Rewrite: replace("pack.samples", " AND g.start_reason IN ('initial', 'handoff')", "")}, check: "PK20",
		},
		{
			name:     "knowledge of another work order",
			sabotage: sqlstore.Sabotage{Rewrite: replace("pack.additions", " AND work_order_id = :work_order_id", " AND :work_order_id = :work_order_id")}, check: "PK17",
		},
		{
			name:     "a generation that reads any lane's baseline",
			sabotage: sqlstore.Sabotage{Rewrite: replace("generation.baseline", " AND generation = :generation", " AND :generation = :generation")}, check: "PK21",
		},
	}
}
