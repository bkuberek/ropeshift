package workorders

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

func said(criterion string, kind VerdictKind) CriterionVerdict {
	return CriterionVerdict{Criterion: criterion, Verdict: kind, Reasons: "because " + criterion, Citations: []string{"main.go:12"}}
}

func recorded(review string, attempt int64, commit string, said ...CriterionVerdict) []Verdict {
	out := make([]Verdict, 0, len(said))
	for _, v := range said {
		out = append(out, Verdict{CriterionVerdict: v, Review: review, WorkOrder: "wo-1", Attempt: attempt, Commit: commit})
	}
	return out
}

func TestAVerdictNeedsACriterionAKindAndReasons(t *testing.T) {
	for name, v := range map[string]CriterionVerdict{
		"no criterion":         {Verdict: VerdictPass, Reasons: "ok"},
		"no kind":              {Criterion: "c1", Reasons: "ok"},
		"a kind nobody offers": {Criterion: "c1", Verdict: "maybe", Reasons: "ok"},
		"no reasons":           {Criterion: "c1", Verdict: VerdictFail},
		"blank reasons":        {Criterion: "c1", Verdict: VerdictPass, Reasons: "  \n"},
		"an empty citation":    {Criterion: "c1", Verdict: VerdictPass, Reasons: "ok", Citations: []string{"main.go:1", " "}},
	} {
		if err := v.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, kind := range []VerdictKind{VerdictPass, VerdictFail, VerdictCannotJudge} {
		if err := said("c1", kind).Validate(); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
	}
}

func TestAReportRefusesAVerdictThatIsNotWhole(t *testing.T) {
	report := Report{Outcome: OutcomeCompleted, Verdicts: []CriterionVerdict{said("c1", VerdictPass), {Criterion: "c2", Verdict: "maybe", Reasons: "x"}}}
	if err := report.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("a malformed verdict: %v", err)
	}
	twice := Report{Outcome: OutcomeCompleted, Verdicts: []CriterionVerdict{said("c1", VerdictPass), said("c1", VerdictFail)}}
	if err := twice.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("a criterion judged twice: %v", err)
	}
	if err := (Report{Outcome: OutcomeCompleted}).Validate(); err != nil {
		t.Errorf("a report with no verdicts, which most lanes make: %v", err)
	}
}

func TestAReviewMustJudgeEveryCriterionOnceAndNoOtherCriterion(t *testing.T) {
	criteria := []string{"c1", "c2"}
	tests := []struct {
		name  string
		said  []CriterionVerdict
		wants string
	}{
		{"every criterion", []CriterionVerdict{said("c1", VerdictPass), said("c2", VerdictFail)}, ""},
		{"in another order", []CriterionVerdict{said("c2", VerdictPass), said("c1", VerdictCannotJudge)}, ""},
		{"one left out", []CriterionVerdict{said("c1", VerdictPass)}, "c2"},
		{"none", nil, "c1"},
		{"a criterion the contract does not have", []CriterionVerdict{said("c1", VerdictPass), said("c2", VerdictPass), said("c9", VerdictPass)}, "c9"},
		{"an unknown one in place of a real one", []CriterionVerdict{said("c1", VerdictPass), said("c9", VerdictPass)}, "c9"},
	}
	for _, tt := range tests {
		err := Report{Outcome: OutcomeCompleted, Verdicts: tt.said}.CheckVerdicts(criteria)
		switch {
		case tt.wants == "" && err != nil:
			t.Errorf("%s: %v", tt.name, err)
		case tt.wants != "" && (!errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.wants)):
			t.Errorf("%s: %v, want it to name %s", tt.name, err, tt.wants)
		}
	}
	if err := (Report{Outcome: OutcomeCompleted}).CheckVerdicts(nil); err != nil {
		t.Errorf("a contract with no criteria needs no verdict: %v", err)
	}
}

func TestVerdictsTravelInAReportAsTheyWereWritten(t *testing.T) {
	report := Report{Outcome: OutcomeCompleted, Verdicts: []CriterionVerdict{said("c1", VerdictFail)}}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Verdicts) != 1 || back.Verdicts[0].Criterion != "c1" || back.Verdicts[0].Verdict != VerdictFail || !slices.Equal(back.Verdicts[0].Citations, []string{"main.go:12"}) {
		t.Errorf("the report came back as %+v", back)
	}
	if !strings.Contains(string(raw), `"verdicts"`) || strings.Contains(string(raw), `"verdicts":null`) {
		t.Errorf("the wire form is %s", raw)
	}
}

func TestOnlyTheCurrentAttemptsCommitIsJudgedByAVerdict(t *testing.T) {
	old := recorded("rv-1", 4, "aaa", said("c1", VerdictFail))
	current := recorded("rv-2", 7, "bbb", said("c1", VerdictPass))
	moved := recorded("rv-3", 7, "ccc", said("c1", VerdictPass))
	same := recorded("rv-4", 9, "bbb", said("c1", VerdictFail))
	all := slices.Concat(old, current, moved, same)

	got := Counted(all, 7, "bbb")
	if len(got) != 1 || got[0].Review != "rv-2" {
		t.Errorf("verdicts that count: %+v", got)
	}
	if got := Counted(all, 7, ""); len(got) != 0 {
		t.Errorf("an attempt with no commit counted %+v", got)
	}
	if got := Counted(all, 9, "bbb"); len(got) != 1 || got[0].Review != "rv-4" {
		t.Errorf("the same commit at another attempt is judged by its own review: %+v", got)
	}
}

func TestReviewRoundsCountReviewsThatGaveVerdicts(t *testing.T) {
	all := slices.Concat(
		recorded("rv-1", 2, "a", said("c1", VerdictFail), said("c2", VerdictPass)),
		recorded("rv-2", 4, "b", said("c1", VerdictFail), said("c2", VerdictPass)),
	)
	if got := Rounds(all); got != 2 {
		t.Errorf("rounds %d, want 2", got)
	}
	if got := Rounds(nil); got != 0 {
		t.Errorf("rounds with no verdict %d", got)
	}
}

func TestWhatVerdictsDecide(t *testing.T) {
	failedCheck := Assessment{Criteria: []CriterionStanding{{Criterion: "c1", Standing: StandingFailed}}, Failed: []string{"ci"}, Needs: NeedMember}
	unchecked := Assessment{Criteria: []CriterionStanding{{Criterion: "c1", Standing: StandingMember}, {Criterion: "c2", Standing: StandingMember}}, Needs: NeedMember}
	coversC2 := Assessment{Criteria: []CriterionStanding{{Criterion: "c1", Standing: StandingMember}, {Criterion: "c2", Standing: StandingCovered}}, Needs: NeedMember}
	onlyLane := Assessment{Criteria: []CriterionStanding{{Criterion: "c1", Standing: StandingSecondPrincipal}, {Criterion: "c2", Standing: StandingMember}}, Needs: NeedMember}

	pass := []CriterionVerdict{said("c1", VerdictPass), said("c2", VerdictPass)}
	tests := []struct {
		name       string
		said       []CriterionVerdict
		assessment Assessment
		level      ledger.MergeAuthority
		rounds     int64
		want       Settlement
		failed     []string
	}{
		{"no verdict decides nothing", nil, unchecked, ledger.ReviewApproves, 0, SettleNone, nil},
		{"every pass accepts at review_approves", pass, unchecked, ledger.ReviewApproves, 1, SettleAccept, nil},
		{"every pass waits for a member at person_merges", pass, unchecked, ledger.PersonMerges, 1, SettleMember, nil},
		{"every pass waits for a member at person_approves", pass, unchecked, ledger.PersonApproves, 1, SettleMember, nil},
		{"a record with no level is person_merges", pass, unchecked, "", 1, SettleMember, nil},
		{"a fail sends the work back at every level", []CriterionVerdict{said("c1", VerdictPass), said("c2", VerdictFail)}, unchecked, ledger.PersonMerges, 1, SettleRework, []string{"c2"}},
		{"a fail sends the work back at review_approves", []CriterionVerdict{said("c1", VerdictFail), said("c2", VerdictFail)}, unchecked, ledger.ReviewApproves, 2, SettleRework, []string{"c1", "c2"}},
		{"a fail at the bound waits for a member", []CriterionVerdict{said("c1", VerdictFail), said("c2", VerdictPass)}, unchecked, ledger.ReviewApproves, 3, SettleMember, nil},
		{"a fail past the bound waits for a member", []CriterionVerdict{said("c1", VerdictFail), said("c2", VerdictPass)}, unchecked, ledger.PersonMerges, 4, SettleMember, nil},
		{"a failed check sends passed work back", pass, failedCheck, ledger.ReviewApproves, 1, SettleRework, nil},
		{"a failed check at the bound waits for a member", pass, failedCheck, ledger.ReviewApproves, 3, SettleMember, nil},
		{"cannot judge with no covering check waits for a member", []CriterionVerdict{said("c1", VerdictPass), said("c2", VerdictCannotJudge)}, unchecked, ledger.ReviewApproves, 1, SettleMember, nil},
		{"a lane's own check does not cover what the review cannot judge", []CriterionVerdict{said("c1", VerdictCannotJudge), said("c2", VerdictPass)}, onlyLane, ledger.ReviewApproves, 1, SettleMember, nil},
		{"a passing forge check covers what the review cannot judge", []CriterionVerdict{said("c1", VerdictPass), said("c2", VerdictCannotJudge)}, coversC2, ledger.ReviewApproves, 1, SettleAccept, nil},
		{"covered cannot judge still waits for a member below review_approves", []CriterionVerdict{said("c1", VerdictPass), said("c2", VerdictCannotJudge)}, coversC2, ledger.PersonMerges, 1, SettleMember, nil},
		{"a fail beside a cannot judge is a fail", []CriterionVerdict{said("c1", VerdictFail), said("c2", VerdictCannotJudge)}, unchecked, ledger.ReviewApproves, 1, SettleRework, []string{"c1"}},
	}
	for _, tt := range tests {
		got := Judge(JudgeInput{
			Criteria: []string{"c1", "c2"}, Verdicts: recorded("rv-1", 3, "abc", tt.said...), Assessment: tt.assessment,
			Authority: tt.level, Rounds: tt.rounds, MaxRounds: 3,
		})
		if got.Settlement != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got.Settlement, tt.want)
			continue
		}
		var failed []string
		for _, v := range got.Failed {
			failed = append(failed, v.Criterion)
		}
		if !slices.Equal(failed, tt.failed) {
			t.Errorf("%s: failed criteria %v, want %v", tt.name, failed, tt.failed)
		}
	}
}

func TestAContractWithNoCriteriaIsNeverApprovedByAReview(t *testing.T) {
	got := Judge(JudgeInput{Verdicts: nil, Assessment: Assessment{Needs: NeedMember}, Authority: ledger.ReviewApproves, MaxRounds: 3})
	if got.Settlement != SettleNone {
		t.Errorf("a review of nothing decided %q", got.Settlement)
	}
}

func TestTheDefaultBoundOnReviewRounds(t *testing.T) {
	if DefaultMaxReviewRounds != 3 {
		t.Errorf("the default bound is %d", DefaultMaxReviewRounds)
	}
	fails := recorded("rv-1", 3, "abc", said("c1", VerdictFail))
	// A bound of zero means the default, because a worker that sent none must not turn rework off.
	if got := Judge(JudgeInput{Criteria: []string{"c1"}, Verdicts: fails, Assessment: Assessment{Needs: NeedMember}, Authority: ledger.PersonMerges, Rounds: 2}); got.Settlement != SettleRework {
		t.Errorf("round 2 of 3 by default: %q", got.Settlement)
	}
	if got := Judge(JudgeInput{Criteria: []string{"c1"}, Verdicts: fails, Assessment: Assessment{Needs: NeedMember}, Authority: ledger.PersonMerges, Rounds: 3}); got.Settlement != SettleMember {
		t.Errorf("round 3 of 3 by default: %q", got.Settlement)
	}
}
