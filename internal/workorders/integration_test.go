package workorders

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

const reviewedCommit = "1111111111111111111111111111111111111111"

func openAt(head string, checks ...ledger.PullRequestCheck) *ledger.PullRequest {
	return &ledger.PullRequest{Number: 7, State: ledger.PullRequestOpen, HeadCommit: head, Checks: checks}
}

func required(name string, state ledger.ForgeCheckState) ledger.PullRequestCheck {
	return ledger.PullRequestCheck{Name: name, State: state, Required: true}
}

// TestTheMergeWaitsForItsApproval holds a merge until an approval binds to the reviewedCommit commit: the review's
// at review_approves, a member's at person_approves.
func TestTheMergeWaitsForItsApproval(t *testing.T) {
	for _, level := range []ledger.MergeAuthority{ledger.PersonApproves, ledger.ReviewApproves} {
		gate := MergeGate(GateInput{Authority: level, Target: ledger.ReviewOnBranch, Commit: reviewedCommit})
		if gate.Step != StepWait || !strings.Contains(gate.Why, "approve") || !strings.Contains(gate.Why, reviewedCommit[:8]) {
			t.Errorf("at %s with no approval the gate says %+v", level, gate)
		}
	}
}

// TestABranchMergesOnceApproved merges a reviewedCommit branch as soon as it is approved: no forge runs checks on
// a branch that was never pushed, so the verdicts and the attempt's recorded checks are the gate.
func TestABranchMergesOnceApproved(t *testing.T) {
	if gate := MergeGate(GateInput{Authority: ledger.ReviewApproves, Target: ledger.ReviewOnBranch, Commit: reviewedCommit, Approved: true}); gate.Step != StepMerge {
		t.Errorf("an approved branch: %+v", gate)
	}
}

// TestAPullRequestMergesOnlyAtTheReviewedHeadWithItsRequiredChecksGreen is FR-MERGE-2 and FR-MERGE-5 as a
// rule: a red required check sends the work back, a pending one waits, a check the base does not require
// decides nothing, and a head other than the reviewedCommit commit, or a closed pull request, waits for a member.
func TestAPullRequestMergesOnlyAtTheReviewedHeadWithItsRequiredChecksGreen(t *testing.T) {
	cases := []struct {
		name string
		pr   *ledger.PullRequest
		step Step
		why  string
	}{
		{"no pull request on record", nil, StepWait, "pull request"},
		{"green", openAt(reviewedCommit, required("CI result", ledger.CheckPassed), ledger.PullRequestCheck{Name: "lint", State: ledger.CheckFailed}), StepMerge, ""},
		{"nothing required", openAt(reviewedCommit), StepMerge, ""},
		{"a required check failed", openAt(reviewedCommit, required("CI result", ledger.CheckFailed), required("unit", ledger.CheckPending)), StepRework, "CI result"},
		{"a required check pending", openAt(reviewedCommit, required("CI result", ledger.CheckPassed), required("unit", ledger.CheckPending)), StepWait, "unit"},
		{"a head that moved", openAt("2222222222222222222222222222222222222222", required("CI result", ledger.CheckPassed)), StepHold, "22222222"},
		{"closed", &ledger.PullRequest{Number: 7, State: ledger.PullRequestClosed, HeadCommit: reviewedCommit}, StepHold, "closed"},
		{"merged", &ledger.PullRequest{Number: 7, State: ledger.PullRequestMerged, HeadCommit: reviewedCommit, MergeCommit: "m"}, StepMerged, ""},
	}
	for _, c := range cases {
		gate := MergeGate(GateInput{Authority: ledger.ReviewApproves, Target: ledger.ReviewOnPullRequest, Commit: reviewedCommit, Approved: true, PullRequest: c.pr})
		if gate.Step != c.step || !strings.Contains(gate.Why, c.why) {
			t.Errorf("%s: the gate says %+v, want %s naming %q", c.name, gate, c.step, c.why)
		}
	}
	gate := MergeGate(GateInput{Authority: ledger.ReviewApproves, Target: ledger.ReviewOnPullRequest, Commit: reviewedCommit, Approved: true,
		PullRequest: openAt(reviewedCommit, required("CI result", ledger.CheckFailed), required("e2e", ledger.CheckFailed))})
	if len(gate.Failed) != 2 || gate.Failed[0] != "CI result" || gate.Failed[1] != "e2e" {
		t.Errorf("the failed checks a rework carries: %v", gate.Failed)
	}
}

// TestAMergedPullRequestEndsTheWorkWhoeverMergedIt reads a pull request merged on the forge as done, approved
// or not, because the work order reaches done when its pull request merges (FR-MERGE-9).
func TestAMergedPullRequestEndsTheWorkWhoeverMergedIt(t *testing.T) {
	merged := &ledger.PullRequest{Number: 7, State: ledger.PullRequestMerged, HeadCommit: reviewedCommit, MergeCommit: "m"}
	if gate := MergeGate(GateInput{Authority: ledger.PersonApproves, Target: ledger.ReviewOnPullRequest, Commit: reviewedCommit, PullRequest: merged}); gate.Step != StepMerged {
		t.Errorf("a pull request the person merged before approving: %+v", gate)
	}
}
