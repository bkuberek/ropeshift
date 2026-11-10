package worker

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A review lane that began anywhere but the commit it judges would read the wrong work and call it the
// right, so it does not start.
func TestAReviewLaneNeedsTheCommitItJudges(t *testing.T) {
	const commit = "c0ffee0000000000000000000000000000000001"
	tests := []struct {
		name    string
		lease   ledger.DesiredLease
		refused bool
	}{
		{"a review that begins at the commit", ledger.DesiredLease{WorkOrder: "rv", Kind: "review", BaseCommit: commit, Attempt: &ledger.DesiredAttempt{Commit: commit}}, false},
		{"a review told nothing of what it judges", ledger.DesiredLease{WorkOrder: "rv", Kind: "review", BaseCommit: commit}, true},
		{"a review of no commit", ledger.DesiredLease{WorkOrder: "rv", Kind: "review", Attempt: &ledger.DesiredAttempt{}}, true},
		{"a review whose claim named no start", ledger.DesiredLease{WorkOrder: "rv", Kind: "review", Attempt: &ledger.DesiredAttempt{Commit: commit}}, true},
		{"a review that begins elsewhere", ledger.DesiredLease{WorkOrder: "rv", Kind: "review", BaseCommit: "another", Attempt: &ledger.DesiredAttempt{Commit: commit}}, true},
		{"a change needs none of it", ledger.DesiredLease{WorkOrder: "wo", Kind: "change"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := (&laneRun{lease: tt.lease}).judgeable(); (err != nil) != tt.refused {
				t.Errorf("got %v, refused is %v", err, tt.refused)
			}
		})
	}
}

// What a lane is held to follows the kind its work order names.
func TestARoleFollowsTheKindOfWork(t *testing.T) {
	tests := []struct {
		lease ledger.DesiredLease
		want  string
	}{
		{ledger.DesiredLease{WorkOrder: "wo", Kind: "change"}, lanes.RoleWriter},
		{ledger.DesiredLease{WorkOrder: "wo", Kind: "rework"}, lanes.RoleWriter},
		{ledger.DesiredLease{WorkOrder: "wo", Kind: "plan"}, lanes.RolePlanner},
		{ledger.DesiredLease{WorkOrder: "rv", Kind: "review"}, lanes.RoleReviewer},
		{ledger.DesiredLease{Lane: "orchestrator-1"}, lanes.RoleOrchestrator},
	}
	for _, tt := range tests {
		if got := (&laneRun{lease: tt.lease}).role(); got != tt.want {
			t.Errorf("a %q lane is a %s, want %s", tt.lease.Kind, got, tt.want)
		}
	}
}

// A review lane's report is held to the contract it reads before the worker takes it, so the lane hears that
// a criterion is missing while it can still say more, and no other lane reports a verdict at all.
func TestAReportIsHeldToTheKindOfWorkItEnds(t *testing.T) {
	judged := func(criteria ...string) workorders.Report {
		report := workorders.Report{Outcome: workorders.OutcomeCompleted}
		for _, id := range criteria {
			report.Verdicts = append(report.Verdicts, workorders.CriterionVerdict{Criterion: id, Verdict: workorders.VerdictPass, Reasons: "it does"})
		}
		return report
	}
	review := ledger.DesiredLease{WorkOrder: "rv", Kind: "review", CriterionIDs: []string{"c1", "c2"}}
	tests := []struct {
		name    string
		lease   ledger.DesiredLease
		report  workorders.Report
		refused string
	}{
		{"a review that judged every criterion", review, judged("c1", "c2"), ""},
		{"a review that left one out", review, judged("c1"), "c2"},
		{"a review that judged one the contract lacks", review, judged("c1", "c2", "c3"), "c3"},
		{"a review that judged none", review, judged(), "c1, c2"},
		{"a review that cannot judge reports no verdict", review, workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: "no tests run here"}, ""},
		{"a change that reports a verdict", ledger.DesiredLease{WorkOrder: "wo", Kind: "change"}, judged("c1"), "only a review"},
		{"a change that reports none", ledger.DesiredLease{WorkOrder: "wo", Kind: "change"}, judged(), ""},
		{"a review of a contract with no criteria", ledger.DesiredLease{WorkOrder: "rv", Kind: "review"}, judged(), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&laneRun{lease: tt.lease}).holdsToItsWork(tt.report)
			switch {
			case tt.refused == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tt.refused != "" && (!errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), tt.refused)):
				t.Errorf("got %v, want a refusal naming %q", err, tt.refused)
			}
		})
	}
}

// The first prompt of a lane that takes reworked work up tells it why the work came back.
func TestTheBriefOfReworkedWorkSaysWhyItCameBack(t *testing.T) {
	lease := ledger.DesiredLease{WorkOrder: "wo", Lane: "lane-2", Kind: "change", Title: "T", Criteria: []string{"c1: It logs."}}
	if brief := (&laneRun{lease: lease}).briefText(); strings.Contains(brief, "came back") || strings.Contains(brief, "review found") {
		t.Errorf("a first attempt is told of a rework:\n%s", brief)
	}
	lease.Rework = &ledger.DesiredRework{
		Review: "rv-1", Reason: "review rv-1 failed criteria c1",
		Findings: []ledger.DesiredFinding{{Criterion: "c1", Text: "It logs.", Reasons: "nothing is logged", Citations: []string{"log.go:3"}}},
	}
	brief := (&laneRun{lease: lease}).briefText()
	for _, want := range []string{"What the review found", "rv-1", "nothing is logged", "log.go:3"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q:\n%s", want, brief)
		}
	}
}

// A lane's profile names the bound on review rounds, and the lane's end reports it.
func TestALaneReportsTheBoundItsProfileSetsOnReviewRounds(t *testing.T) {
	run := &laneRun{}
	if got := run.reviewBound(); got != 0 {
		t.Errorf("a lane that read no profile reports %d", got)
	}
	run.noteReviewBound(lanes.Profile{Review: lanes.ReviewSettings{MaxReviewRounds: 4}})
	if got := run.reviewBound(); got != 4 {
		t.Errorf("a profile of 4 rounds reports %d", got)
	}
	run.noteReviewBound(lanes.Profile{})
	if got := run.reviewBound(); got != 4 {
		t.Errorf("a profile that sets none must not undo the one a lane read: %d", got)
	}
}
