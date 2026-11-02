//go:build chaos

package chaos

import (
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/test/contract"
)

// checkClaimsPinned holds every claim to a pack that exists, whichever side of a kill it stands on.
func checkClaimsPinned(w *world) error {
	return contract.ClaimsPinned(w.rig, w.store)
}

// packVariants are the broken packs the pack checks must catch.
func packVariants() []variant {
	return []variant{
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
