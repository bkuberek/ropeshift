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

// ADR-0040: a lane's own settings are compared with the commit its work began from, the claim's base, and never with
// the tip of the branch it continues, which a lane wrote: a lane that committed its own hooks edit would otherwise find
// nothing changed at its next generation.
func TestALaneThatContinuesABranchComparesItsSettingsAtTheClaimsBase(t *testing.T) {
	continued := &laneRun{lease: ledger.DesiredLease{
		WorkOrder: "wo", Kind: "change", BaseRef: "origin/main", BaseCommit: "fork",
		Continues: "feature/retry", ContinuesRef: "origin/feature/retry", ContinuesCommit: "tip",
	}}
	if got := continued.settingsBase(); got != "fork" {
		t.Errorf("a continued lane compares its settings at %q", got)
	}
	plain := &laneRun{lease: ledger.DesiredLease{WorkOrder: "wo", Kind: "change", BaseCommit: "fork"}}
	if got := plain.settingsBase(); got != "fork" {
		t.Errorf("a lane that continues nothing compares its settings at %q", got)
	}
	unresolved := &laneRun{lease: ledger.DesiredLease{WorkOrder: "wo", Kind: "change", Continues: "feature/retry", ContinuesCommit: "tip"}}
	if got := unresolved.settingsBase(); got != "tip" {
		t.Errorf("a claim that resolved no base compares its settings at %q", got)
	}
}

// ADR-0040: a review lane begins at the commit under review, which its writer wrote, so its settings are those of the
// commit the reviewed lane began from, where the review's diff starts, and never the commit under review.
func TestAReviewLaneComparesItsSettingsAtTheCommitTheReviewedLaneBeganFrom(t *testing.T) {
	review := &laneRun{lease: ledger.DesiredLease{
		WorkOrder: "rv", Kind: "review", BaseCommit: "head",
		Attempt: &ledger.DesiredAttempt{Commit: "head", BaseCommit: "fork"},
	}}
	if got := review.begins(); got != "head" {
		t.Errorf("a review begins at %q", got)
	}
	if got := review.settingsBase(); got != "fork" {
		t.Errorf("a review compares its settings at %q", got)
	}
	unrecorded := &laneRun{lease: ledger.DesiredLease{
		WorkOrder: "rv", Kind: "review", BaseCommit: "head", Attempt: &ledger.DesiredAttempt{Commit: "head"},
	}}
	if got := unrecorded.settingsBase(); got != "" {
		t.Errorf("a review of a lane that recorded no base compares its settings at %q", got)
	}
}
