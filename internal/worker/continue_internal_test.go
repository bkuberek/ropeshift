package worker

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

// ADR-0023: a lane that continues an in-flight branch begins its own branch at that branch's tip, keeps the
// branch from every push and move, and is told where its work is measured from.
func TestALaneThatContinuesABranchBeginsAtItsTip(t *testing.T) {
	continued := &laneRun{lease: ledger.DesiredLease{
		WorkOrder: "wo", Kind: "change", BaseRef: "origin/main", BaseCommit: "fork",
		Continues: "feature/retry", ContinuesRef: "origin/feature/retry", ContinuesCommit: "tip",
	}}
	if got := continued.begins(); got != "tip" {
		t.Errorf("a continued lane begins at %q", got)
	}
	if got := continued.kept(); !slices.Equal(got, []string{"feature/retry"}) {
		t.Errorf("a continued lane keeps %v", got)
	}
	if got := continued.continuation(); got == nil || got.Branch != "feature/retry" || got.Tip != "tip" || got.Base != "fork" {
		t.Errorf("a continued lane is told %+v", got)
	}
	plain := &laneRun{lease: ledger.DesiredLease{WorkOrder: "wo", Kind: "change", BaseRef: "origin/main", BaseCommit: "fork"}}
	if plain.begins() != "fork" || plain.kept() != nil || plain.continuation() != nil {
		t.Errorf("a lane that continues nothing begins at %q, keeps %v, and is told %+v", plain.begins(), plain.kept(), plain.continuation())
	}
}
