package workorders

import (
	"errors"
	"testing"
)

func TestAReviewOutcomeIsFailIfAnyCriterionFailedAndPassOnlyIfAllPassed(t *testing.T) {
	for name, tt := range map[string]struct {
		said []CriterionVerdict
		want ReviewOutcome
	}{
		"all pass":                  {[]CriterionVerdict{said("c1", VerdictPass), said("c2", VerdictPass)}, ReviewPass},
		"one fails":                 {[]CriterionVerdict{said("c1", VerdictPass), said("c2", VerdictFail)}, ReviewFail},
		"a fail beats a cannot":     {[]CriterionVerdict{said("c1", VerdictCannotJudge), said("c2", VerdictFail)}, ReviewFail},
		"a cannot with passes":      {[]CriterionVerdict{said("c1", VerdictPass), said("c2", VerdictCannotJudge)}, ReviewCannotJudge},
		"every criterion is cannot": {[]CriterionVerdict{said("c1", VerdictCannotJudge)}, ReviewCannotJudge},
	} {
		if got := ReviewOutcomeOf(recorded("r1", 1, "abc", tt.said...)); got != tt.want {
			t.Errorf("%s: %q, want %q", name, got, tt.want)
		}
	}
	if got := ReviewOutcomeOf(nil); got != "" {
		t.Errorf("no verdicts read as %q, and a review that judged nothing has no outcome", got)
	}
}

func TestAMemberAgreesWhenTheDecisionFollowsTheVerdict(t *testing.T) {
	for _, tt := range []struct {
		outcome  ReviewOutcome
		decision CalibrationDecision
		want     Agreement
	}{
		{ReviewPass, DecisionAccept, AgreementAgree},
		{ReviewPass, DecisionRework, AgreementDisagree},
		{ReviewFail, DecisionRework, AgreementAgree},
		{ReviewFail, DecisionAccept, AgreementDisagree},
		{ReviewCannotJudge, DecisionAccept, AgreementUnknown},
		{ReviewCannotJudge, DecisionRework, AgreementUnknown},
	} {
		if got := AgreementOf(tt.outcome, tt.decision); got != tt.want {
			t.Errorf("%s then %s: %q, want %q", tt.outcome, tt.decision, got, tt.want)
		}
	}
}

func TestACalibrationRecordIsBoundToItsReviewAndItsCommit(t *testing.T) {
	whole := Calibration{
		ID: "c1", WorkOrder: "wo-1", Review: "r1", Commit: "abc", Outcome: ReviewPass, Decision: DecisionAccept,
		Agreement: AgreementAgree, Source: SourceDecision,
	}
	if err := whole.Validate(); err != nil {
		t.Fatalf("a whole record: %v", err)
	}
	for name, mutate := range map[string]func(*Calibration){
		"no work order":         func(c *Calibration) { c.WorkOrder = "" },
		"no review":             func(c *Calibration) { c.Review = "" },
		"no commit":             func(c *Calibration) { c.Commit = "" },
		"no outcome":            func(c *Calibration) { c.Outcome = "" },
		"an outcome nobody has": func(c *Calibration) { c.Outcome = "maybe" },
		"a decision nobody has": func(c *Calibration) { c.Decision = "ignore" },
		"no source":             func(c *Calibration) { c.Source = "" },
		"agreement that does not follow": func(c *Calibration) {
			c.Agreement = AgreementDisagree
		},
	} {
		c := whole
		mutate(&c)
		if err := c.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestALaterDefectOnlyDisagreesWithAPass(t *testing.T) {
	defect := Calibration{
		ID: "c1", WorkOrder: "wo-1", Review: "r1", Commit: "abc", Outcome: ReviewPass, Decision: DecisionRework,
		Agreement: AgreementDisagree, Source: SourceLaterDefect,
	}
	if err := defect.Validate(); err != nil {
		t.Fatalf("a defect found in approved work: %v", err)
	}
	defect.Outcome, defect.Agreement = ReviewFail, AgreementAgree
	if err := defect.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("a defect found in work the review failed is no later defect: %v", err)
	}
	accepted := defect
	accepted.Outcome, accepted.Agreement, accepted.Decision = ReviewPass, AgreementAgree, DecisionAccept
	if err := accepted.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("a later defect is a rework: %v", err)
	}
}
