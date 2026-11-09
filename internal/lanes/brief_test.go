package lanes

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A session has no document of the format, so a planning lane's brief carries it whole, and says
// what the ledger takes from it.
func TestAPlanningBriefCarriesTheFormat(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan})
	for _, want := range []string{planning.FormatExample, "Commit the files on this branch", "accepts your work", ToolLaneReport} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q", want)
		}
	}
}

func TestAReviewBriefCarriesThePlanAndAsksForEverything(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Review it", Kind: workorders.KindContractReview, Review: &ledger.DesiredReview{
		Plan: "The plan",
		Contracts: []ledger.ReviewedContract{
			{Title: "Alpha", Statement: "Retry twice.", Criteria: []string{"c1: It retries."}},
			{Title: "Beta", Statement: "Log it."},
		},
	}})
	for _, want := range []string{
		"Plan: The plan", "Contract: Alpha", "Retry twice.", "c1: It retries.", "Contract: Beta", "Log it.", ToolAsk, "blocking", "Do not change any file",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q", want)
		}
	}
}

// Work that plans nothing and reviews nothing reads as it always has.
func TestAChangeIsToldNeither(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Change it", Kind: workorders.KindChange})
	for _, absent := range []string{"front matter", "contract review"} {
		if strings.Contains(brief, absent) {
			t.Errorf("a change's brief says %q", absent)
		}
	}
}
