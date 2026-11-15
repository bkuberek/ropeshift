package workorders

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

func check(criterion string, source EvidenceSource, status CheckStatus) Evidence {
	return Evidence{Name: "check-" + criterion, Criterion: criterion, Source: source, Status: status}
}

func TestAssessFollowsTheAcceptanceTable(t *testing.T) {
	tests := []struct {
		name     string
		criteria []string
		evidence []Evidence
		want     []Standing
		needs    Need
		failed   int
	}{
		{
			name: "a forge result counts by itself", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceForge, CheckPassed)},
			want:     []Standing{StandingCovered}, needs: NeedNothing,
		},
		{
			name: "a check only the lane reports needs a second principal", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceLane, CheckPassed)},
			want:     []Standing{StandingSecondPrincipal}, needs: NeedSecondPrincipal,
		},
		{
			name: "a criterion no check maps to needs a member", criteria: []string{"c1"},
			want: []Standing{StandingMember}, needs: NeedMember,
		},
		{
			name: "a forge result stands beside the lane's own word", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceLane, CheckPassed), check("c1", SourceForge, CheckPassed)},
			want:     []Standing{StandingCovered}, needs: NeedNothing,
		},
		{
			name: "a failed forge result fails the criterion", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceForge, CheckFailed)},
			want:     []Standing{StandingFailed}, needs: NeedMember, failed: 1,
		},
		{
			name: "a failed lane check fails the criterion", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceLane, CheckFailed)},
			want:     []Standing{StandingFailed}, needs: NeedMember, failed: 1,
		},
		{
			name: "a pass does not outweigh a failure", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceForge, CheckPassed), check("c1", SourceLane, CheckFailed)},
			want:     []Standing{StandingFailed}, needs: NeedMember, failed: 1,
		},
		{
			name: "a check naming no criterion covers nothing", criteria: []string{"c1"},
			evidence: []Evidence{check("", SourceForge, CheckPassed)},
			want:     []Standing{StandingMember}, needs: NeedMember,
		},
		{
			name: "a check naming another criterion covers nothing", criteria: []string{"c1"},
			evidence: []Evidence{check("c9", SourceForge, CheckPassed)},
			want:     []Standing{StandingMember}, needs: NeedMember,
		},
		{
			name: "a failed check with no criterion still failed", criteria: []string{"c1"},
			evidence: []Evidence{check("c1", SourceForge, CheckPassed), check("", SourceLane, CheckFailed)},
			want:     []Standing{StandingCovered}, needs: NeedMember, failed: 1,
		},
		{
			name: "the weakest evidence decides who is needed", criteria: []string{"c1", "c2"},
			evidence: []Evidence{check("c1", SourceForge, CheckPassed), check("c2", SourceLane, CheckPassed)},
			want:     []Standing{StandingCovered, StandingSecondPrincipal}, needs: NeedSecondPrincipal,
		},
		{
			name: "one criterion with no check needs a member for the whole work order", criteria: []string{"c1", "c2", "c3"},
			evidence: []Evidence{check("c1", SourceForge, CheckPassed), check("c2", SourceLane, CheckPassed)},
			want:     []Standing{StandingCovered, StandingSecondPrincipal, StandingMember}, needs: NeedMember,
		},
		{
			name: "no criteria at all counts for nothing", needs: NeedMember,
			evidence: []Evidence{check("c1", SourceForge, CheckPassed)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Assess(tt.criteria, tt.evidence)
			var standings []Standing
			for i, row := range got.Criteria {
				if row.Criterion != tt.criteria[i] {
					t.Errorf("row %d names %s", i, row.Criterion)
				}
				standings = append(standings, row.Standing)
			}
			if !slices.Equal(standings, tt.want) {
				t.Errorf("standings %v, want %v", standings, tt.want)
			}
			if got.Needs != tt.needs {
				t.Errorf("needs %s, want %s", got.Needs, tt.needs)
			}
			if len(got.Failed) != tt.failed {
				t.Errorf("failed %v, want %d", got.Failed, tt.failed)
			}
		})
	}
}

// TestVerifiedWorkPassedEveryCriterionWithEveryRequiredCheckGreen holds the only acceptance a delegate policy
// may leave without a card: the review of the attempt has ruled, every criterion passed on a check the lane did
// not write, nothing failed, no verdict that counts failed or left a criterion unjudged, and every check the
// base requires is green on the pull request the work lands through.
func TestVerifiedWorkPassedEveryCriterionWithEveryRequiredCheckGreen(t *testing.T) {
	covered := Assess([]string{"c1", "c2"}, []Evidence{check("c1", SourceForge, CheckPassed), check("c2", SourceForge, CheckPassed)})
	green := ledger.PullRequestCheck{Name: "CI result", State: ledger.CheckPassed, Required: true}
	optional := ledger.PullRequestCheck{Name: "lint", State: ledger.CheckFailed}
	verdict := func(criterion string, kind VerdictKind) Verdict {
		return Verdict{CriterionVerdict: CriterionVerdict{Criterion: criterion, Verdict: kind}}
	}
	tests := []struct {
		name        string
		assessment  Assessment
		ruled       bool
		verdicts    []Verdict
		pullRequest []ledger.PullRequestCheck
		want        bool
	}{
		{"every criterion covered, no pull request", covered, true, nil, nil, true},
		{"every criterion covered, every required check green", covered, true, nil, []ledger.PullRequestCheck{green, optional}, true},
		{"every criterion covered, the review has not ruled", covered, false, nil, []ledger.PullRequestCheck{green}, false},
		{"a required check still running", covered, true, nil, []ledger.PullRequestCheck{green, {Name: "e2e", State: ledger.CheckPending, Required: true}}, false},
		{"a required check failed", covered, true, nil, []ledger.PullRequestCheck{{Name: "e2e", State: ledger.CheckFailed, Required: true}}, false},
		{"a criterion only the lane vouches for", Assess([]string{"c1", "c2"}, []Evidence{check("c1", SourceForge, CheckPassed), check("c2", SourceLane, CheckPassed)}), true, nil, nil, false},
		{"a criterion no check maps to", Assess([]string{"c1", "c2"}, []Evidence{check("c1", SourceForge, CheckPassed)}), true, nil, nil, false},
		{"a failed check", Assess([]string{"c1"}, []Evidence{check("c1", SourceForge, CheckPassed), {Name: "vet", Status: CheckFailed, Source: SourceForge}}), true, nil, nil, false},
		{"no criteria", Assess(nil, nil), true, nil, nil, false},
		{"every criterion covered, every verdict passed", covered, true, []Verdict{verdict("c1", VerdictPass), verdict("c2", VerdictPass)}, nil, true},
		{"every criterion covered, a verdict failed", covered, true, []Verdict{verdict("c1", VerdictPass), verdict("c2", VerdictFail)}, nil, false},
		{"every criterion covered, a criterion the review could not judge", covered, true, []Verdict{verdict("c1", VerdictCannotJudge), verdict("c2", VerdictPass)}, nil, false},
	}
	for _, tt := range tests {
		if got := Verified(tt.assessment, tt.ruled, tt.verdicts, tt.pullRequest); got != tt.want {
			t.Errorf("%s: verified %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestTheReviewOfAnAttemptRulesOnItOnceItsVerdictsCount holds when the review of an attempt has ruled: a change or
// a rework that left a commit has a review of it, which rules once no review of the attempt is open and verdicts
// count for it. A review of an earlier attempt rules on nothing here, and work no review judges needs none.
func TestTheReviewOfAnAttemptRulesOnItOnceItsVerdictsCount(t *testing.T) {
	change := WorkOrder{ID: "w1", Kind: KindChange, Fence: 3}
	review := func(state State, attempt int64) WorkOrder {
		return WorkOrder{ID: "r", Kind: KindReview, State: state, Reviews: "w1", ReviewsFence: attempt}
	}
	counted := []Verdict{{CriterionVerdict: CriterionVerdict{Criterion: "c1", Verdict: VerdictPass}, Attempt: 3, Commit: "abc"}}
	tests := []struct {
		name     string
		work     WorkOrder
		commit   string
		reviews  []WorkOrder
		verdicts []Verdict
		want     bool
	}{
		{"a review of the attempt is still open", change, "abc", []WorkOrder{review(Running, 3)}, nil, false},
		{"a review of the attempt is open beside verdicts that count", change, "abc", []WorkOrder{review(Done, 3), review(Ready, 3)}, counted, false},
		{"the review ended with no verdict", change, "abc", []WorkOrder{review(Failed, 3)}, nil, false},
		{"the review ended with verdicts that count", change, "abc", []WorkOrder{review(Done, 3)}, counted, true},
		{"only a review of an earlier attempt is open", change, "abc", []WorkOrder{review(Running, 2), review(Done, 3)}, counted, true},
		{"a rework at a commit no review judged", WorkOrder{ID: "w1", Kind: KindRework, Fence: 3}, "abc", nil, nil, false},
		{"an attempt that left no commit", change, "", nil, nil, true},
		{"a contract review at a commit, which no review judges", WorkOrder{ID: "w1", Kind: KindContractReview, Fence: 3}, "abc", nil, nil, true},
	}
	for _, tt := range tests {
		if got := Ruled(tt.work, tt.commit, tt.reviews, tt.verdicts); got != tt.want {
			t.Errorf("%s: ruled %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestAFailedVerdictIsOneThatCounts holds the only rework a delegate policy may leave without a card: one a
// failed verdict of the attempt stands behind.
func TestAFailedVerdictIsOneThatCounts(t *testing.T) {
	verdict := func(kind VerdictKind) Verdict {
		return Verdict{CriterionVerdict: CriterionVerdict{Criterion: "c1", Verdict: kind}}
	}
	for _, tt := range []struct {
		name     string
		verdicts []Verdict
		want     bool
	}{
		{"no verdict", nil, false},
		{"every verdict passed", []Verdict{verdict(VerdictPass)}, false},
		{"a criterion no review could judge", []Verdict{verdict(VerdictCannotJudge)}, false},
		{"a verdict failed", []Verdict{verdict(VerdictPass), verdict(VerdictFail)}, true},
	} {
		if got := (AcceptanceView{Verdicts: tt.verdicts}).FailedVerdict(); got != tt.want {
			t.Errorf("%s: a failed verdict %v, want %v", tt.name, got, tt.want)
		}
	}
}
