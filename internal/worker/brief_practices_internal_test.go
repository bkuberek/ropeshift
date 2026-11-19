package worker

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

// A lane is briefed with what its lease carries of the practices check: a planning lane is told each repository's
// findings, and a lane of any other kind is told none (FR-PRAC-8).
func TestTheBriefOfAPlanningLaneCarriesWhatItsLeaseHoldsOfThePracticesCheck(t *testing.T) {
	summaries := []ledger.PracticesSummary{{
		Repository: "repo-1", Name: "widgets", Checked: true, Commit: "1111111", AgeMs: 60_000,
		Open: []ledger.PracticesOpenFinding{{ID: "codeowners", Severity: "low", Detail: "no CODEOWNERS file", Fix: "Commit CODEOWNERS."}},
	}}
	plan := ledger.DesiredLease{WorkOrder: "wo", Lane: "lane-1", Kind: "plan", Title: "Plan", Practices: summaries}
	brief := (&laneRun{lease: plan, rt: &LaneRuntime{}}).briefText()
	for _, want := range []string{"Practices findings", "Repository widgets, checked at commit 1111111, 1m ago.", "- codeowners (low): no CODEOWNERS file"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the planning brief leaves out %q", want)
		}
	}
	change := ledger.DesiredLease{WorkOrder: "wo", Lane: "lane-1", Kind: "change", Title: "Change", Practices: summaries}
	if brief := (&laneRun{lease: change, rt: &LaneRuntime{}}).briefText(); strings.Contains(brief, "Practices findings") {
		t.Errorf("a change lane is told the findings:\n%s", brief)
	}
}
