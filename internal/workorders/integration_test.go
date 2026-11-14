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
		{"a head that moved", openAt("2222222222222222222222222222222222222222", required("CI result", ledger.CheckPassed)), StepReview, "22222222"},
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

// TestAMovedHeadIsReviewedAgain sends a pull request whose head moved past the reviewed commit back to review
// at the new head, whatever the level and whether or not anyone approved the old one (ADR-0020): an approval
// binds to the commit it approved, and ropeshift merges only a commit a review judged.
func TestAMovedHeadIsReviewedAgain(t *testing.T) {
	moved := "3333333333333333333333333333333333333333"
	for _, level := range []ledger.MergeAuthority{ledger.PersonMerges, ledger.PersonApproves, ledger.ReviewApproves} {
		for _, approved := range []bool{false, true} {
			gate := MergeGate(GateInput{Authority: level, Target: ledger.ReviewOnPullRequest, Commit: reviewedCommit, Approved: approved, PullRequest: openAt(moved)})
			if gate.Step != StepReview || gate.Head != moved {
				t.Errorf("at %s, approved %v, a moved head: %+v", level, approved, gate)
			}
		}
	}
}

// TestAtPersonMergesThePersonMergesThePullRequest never merges at person_merges: accepted work waits for the
// person's merge on the forge, which ends it, while a failed required check still sends it back and a closed
// pull request waits for the person.
func TestAtPersonMergesThePersonMergesThePullRequest(t *testing.T) {
	in := func(pr *ledger.PullRequest, approved bool) Gate {
		return MergeGate(GateInput{Authority: ledger.PersonMerges, Target: ledger.ReviewOnPullRequest, Commit: reviewedCommit, Approved: approved, PullRequest: pr})
	}
	for _, approved := range []bool{false, true} {
		if gate := in(openAt(reviewedCommit, required("CI result", ledger.CheckPassed)), approved); gate.Step != StepWait || !strings.Contains(gate.Why, "#7") {
			t.Errorf("approved %v, green: %+v", approved, gate)
		}
	}
	if gate := in(openAt(reviewedCommit, required("CI result", ledger.CheckFailed)), false); gate.Step != StepRework {
		t.Errorf("a failed required check: %+v", gate)
	}
	if gate := in(&ledger.PullRequest{Number: 7, State: ledger.PullRequestClosed, HeadCommit: reviewedCommit}, false); gate.Step != StepWait || !strings.Contains(gate.Why, "closed") {
		t.Errorf("a closed pull request: %+v", gate)
	}
	if gate := in(&ledger.PullRequest{Number: 7, State: ledger.PullRequestMerged, HeadCommit: reviewedCommit}, false); gate.Step != StepMerged {
		t.Errorf("the person's merge: %+v", gate)
	}
}

// TestAPlanningBranchMergesOnItsPlanApproval holds a planning branch until a member's plan approval binds to
// its commit, and never asks for a merge approval of its own (ADR-0018, ADR-0020): the plan approval is the
// merge approval. A plan on the branch whose approval no member decided holds the merge, even once another
// plan on it was approved.
func TestAPlanningBranchMergesOnItsPlanApproval(t *testing.T) {
	for _, level := range []ledger.MergeAuthority{ledger.PersonApproves, ledger.ReviewApproves} {
		for _, target := range []ledger.ReviewTarget{ledger.ReviewOnBranch, ledger.ReviewOnPullRequest} {
			in := GateInput{Authority: level, Target: target, Commit: reviewedCommit, Plan: &PlanStanding{}, PullRequest: openAt(reviewedCommit)}
			gate := MergeGate(in)
			if gate.Step != StepWait || !strings.Contains(gate.Why, "plan approval") || !strings.Contains(gate.Why, reviewedCommit[:8]) {
				t.Errorf("at %s on %s with no plan approval: %+v", level, target, gate)
			}
			in.Plan.Waiting = []string{"plan budget version 2"}
			if gate := MergeGate(in); gate.Step != StepWait || !strings.Contains(gate.Why, "plan budget version 2") {
				t.Errorf("at %s on %s with a plan waiting: %+v", level, target, gate)
			}
			in.Approved = true
			if gate := MergeGate(in); gate.Step != StepWait || !strings.Contains(gate.Why, "plan budget version 2") {
				t.Errorf("at %s on %s, approved with another plan waiting: %+v", level, target, gate)
			}
			in.Plan.Waiting = nil
			if gate := MergeGate(in); gate.Step != StepMerge {
				t.Errorf("at %s on %s, approved: %+v", level, target, gate)
			}
		}
	}
}

// TestAPlanningPullRequestFollowsItsPullRequest reads a planning branch's pull request as any other's: a head
// that moved goes back to acceptance at the new head, whose plan needs its own approval, and the person's merge
// on the forge ends the work, approved or not. At person_merges the person merges it.
func TestAPlanningPullRequestFollowsItsPullRequest(t *testing.T) {
	moved := "3333333333333333333333333333333333333333"
	in := GateInput{Authority: ledger.ReviewApproves, Target: ledger.ReviewOnPullRequest, Commit: reviewedCommit, Approved: true, Plan: &PlanStanding{}, PullRequest: openAt(moved)}
	if gate := MergeGate(in); gate.Step != StepReview || gate.Head != moved {
		t.Errorf("a moved head: %+v", gate)
	}
	in.Approved, in.PullRequest = false, &ledger.PullRequest{Number: 7, State: ledger.PullRequestMerged, HeadCommit: reviewedCommit, MergeCommit: "m"}
	if gate := MergeGate(in); gate.Step != StepMerged {
		t.Errorf("a planning pull request the person merged: %+v", gate)
	}
	in.Authority, in.Approved, in.PullRequest = ledger.PersonMerges, true, openAt(reviewedCommit)
	if gate := MergeGate(in); gate.Step != StepWait || !strings.Contains(gate.Why, "#7") {
		t.Errorf("at person_merges: %+v", gate)
	}
}

// TestApprovedWorkMergesAfterItsDependencies holds the merge of approved work while any work it depends on has
// not merged, in its own repository or another, naming each one (ADR-0020, FR-REPO-3). At person_merges the
// person merges the pull request, so the wait only tells them to merge the dependencies first.
func TestApprovedWorkMergesAfterItsDependencies(t *testing.T) {
	unmerged := []Unmerged{{WorkOrder: "wo-api", Repository: "api", State: Integrating}, {WorkOrder: "wo-lib", State: Acceptance}, {WorkOrder: "missing"}}
	for _, in := range []GateInput{
		{Authority: ledger.ReviewApproves, Target: ledger.ReviewOnBranch, Commit: reviewedCommit, Approved: true, Unmerged: unmerged},
		{Authority: ledger.PersonApproves, Target: ledger.ReviewOnPullRequest, Commit: reviewedCommit, Approved: true, PullRequest: openAt(reviewedCommit), Unmerged: unmerged},
		{Authority: ledger.PersonMerges, Target: ledger.ReviewOnPullRequest, Commit: reviewedCommit, PullRequest: openAt(reviewedCommit), Unmerged: unmerged},
	} {
		gate := MergeGate(in)
		if gate.Step != StepWait || len(gate.Unmerged) != 3 {
			t.Errorf("at %s on %s with work it depends on unmerged the gate says %+v", in.Authority, in.Target, gate)
		}
		for _, want := range []string{"wo-api in api is integrating", "wo-lib is acceptance", "missing is not known"} {
			if !strings.Contains(gate.Why, want) {
				t.Errorf("at %s on %s the wait lacks %q: %s", in.Authority, in.Target, want, gate.Why)
			}
		}
	}
	// Approval comes first: a member is asked to approve before the merge waits for the dependencies.
	if gate := MergeGate(GateInput{Authority: ledger.PersonApproves, Target: ledger.ReviewOnBranch, Commit: reviewedCommit, Unmerged: unmerged}); gate.Step != StepWait || !strings.Contains(gate.Why, "approve") || len(gate.Unmerged) != 0 {
		t.Errorf("unapproved work with unmerged dependencies: %+v", gate)
	}
	// What decides the work before any merge still decides it: a merge on the forge, a failed check, a moved head.
	for _, pr := range []*ledger.PullRequest{
		{Number: 7, State: ledger.PullRequestMerged, HeadCommit: reviewedCommit},
		openAt(reviewedCommit, required("ci", ledger.CheckFailed)),
		openAt("2222222222222222222222222222222222222222"),
	} {
		gate := MergeGate(GateInput{Authority: ledger.ReviewApproves, Target: ledger.ReviewOnPullRequest, Commit: reviewedCommit, Approved: true, PullRequest: pr, Unmerged: unmerged})
		if gate.Step == StepWait || gate.Step == StepMerge {
			t.Errorf("a pull request %+v with unmerged dependencies: %+v", pr, gate)
		}
	}
	if gate := MergeGate(GateInput{Authority: ledger.ReviewApproves, Target: ledger.ReviewOnBranch, Commit: reviewedCommit, Approved: true}); gate.Step != StepMerge {
		t.Errorf("approved work with every dependency merged: %+v", gate)
	}
}
