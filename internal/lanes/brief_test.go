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
	for _, want := range []string{"Commit the files on this branch", "accepts your work", ToolLaneReport} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q", want)
		}
	}
}

// The person approves a plan in one step, and the lane that wrote it cannot (FR-PLAN-12). It is told what
// that step does, so it does not wait for a review per work order, and that a change after it is a new
// version.
func TestAPlanningBriefSaysHowThePlanIsApproved(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan})
	for _, want := range []string{
		"approves the plan in one step", "as their decisions", "contract review", "cannot approve the plan yourself", "new version of the plan",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q", want)
		}
	}
}

// A planning lane writes every format it is shown, so it is shown all of them: each document kind,
// milestones, contracts with their work orders, and the draft decisions of a plan (FR-SESSION-5).
func TestAPlanningBriefShowsEveryFormat(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan})
	if len(planning.FormatExamples) < 8 {
		t.Fatalf("%d examples", len(planning.FormatExamples))
	}
	for _, example := range planning.FormatExamples {
		if !strings.Contains(brief, example.Text) {
			t.Errorf("the brief leaves out the %s example", example.Kind)
		}
		if !strings.Contains(brief, example.Intro) {
			t.Errorf("the brief leaves out what the %s example is for", example.Kind)
		}
	}
	for _, want := range []string{
		"[[decision]]", "[[exit_criteria]]", "[[work_orders]]", "[[requirement]]", "assets", "depends_on", "shared", "uses", "documents",
		"kind:id", "binds nothing until", "no requirement", "version",
		"is not a milestone of its plan", "marked shared", "form a cycle", "wait for", "every contract of it names one",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief says nothing of %q", want)
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

func reviewedAttempt() *ledger.DesiredAttempt {
	return &ledger.DesiredAttempt{
		WorkOrder: "wo-1", Title: "Add the retry budget", Branch: "lane/lane-1", Commit: "c0ffee0000000000000000000000000000000001",
		BaseRef: "origin/main", BaseCommit: "ba5e000000000000000000000000000000000002",
	}
}

// A review lane is told what it judges, that it may change nothing, where the diff is, and how to
// report: one check for each criterion.
func TestAReviewBriefSaysWhatIsJudgedAndWhatIsForbidden(t *testing.T) {
	brief := Brief(BriefSpec{
		WorkOrder: "rv-1", Title: "Review: Add the retry budget", Statement: "Retry twice.", Criteria: []string{"c1: It retries.", "c2: It logs."},
		Pack: true, Kind: workorders.KindReview, Attempt: reviewedAttempt(),
	})
	for _, want := range []string{
		"work order wo-1", "Add the retry budget", "c0ffee0000000000000000000000000000000001", "lane/lane-1",
		"ba5e000000000000000000000000000000000002", "origin/main", "You did not write it",
		"may not edit a file with a tool, commit, push, or move a branch", ReviewDiffFile, ReviewFilesFile,
		"c1: It retries.", "c2: It logs.", "requirements and documents the contract cites", "information and not evidence",
		"one check for each criterion", ToolLaneReport, "criterion field",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q:\n%s", want, brief)
		}
	}
}

// A review of work that recorded no base has no diff, and the brief says so and sends the reviewer to
// the history instead of naming a file that holds nothing.
func TestAReviewBriefWithNoRecordedBaseHasNoDiff(t *testing.T) {
	attempt := reviewedAttempt()
	attempt.BaseRef, attempt.BaseCommit = "", ""
	brief := Brief(BriefSpec{WorkOrder: "rv-1", Title: "Review", Kind: workorders.KindReview, Attempt: attempt})
	if !strings.Contains(brief, "recorded no base") || !strings.Contains(brief, "git log") || strings.Contains(brief, ReviewFilesFile) {
		t.Errorf("the brief reads:\n%s", brief)
	}
}

// Every other lane reads as it always has.
func TestAChangeIsNotToldItReviews(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Change it", Kind: workorders.KindChange})
	for _, absent := range []string{"This is a review", ReviewDiffFile, "you may not edit"} {
		if strings.Contains(brief, absent) {
			t.Errorf("a change's brief says %q", absent)
		}
	}
}
