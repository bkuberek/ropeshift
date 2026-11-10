package workorders

import (
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
)

// ReviewOutcome is what a review found of the work as a whole, read from its verdicts on the criteria
// (FR-CAL-1). It is not a stored judgment of the review: a review gives a verdict per criterion, and this
// is how a member's single decision is compared with them.
type ReviewOutcome string

// The outcomes a review's verdicts add up to.
const (
	// ReviewPass means every criterion passed.
	ReviewPass ReviewOutcome = "pass"
	// ReviewFail means at least one criterion failed.
	ReviewFail ReviewOutcome = "fail"
	// ReviewCannotJudge means nothing failed and at least one criterion could not be judged.
	ReviewCannotJudge ReviewOutcome = "cannot_judge"
)

// ReviewOutcomeOf adds one review's verdicts up. A fail beats a criterion the review could not judge,
// because the work goes back for it either way. No verdicts have no outcome.
func ReviewOutcomeOf(verdicts []Verdict) ReviewOutcome {
	if len(verdicts) == 0 {
		return ""
	}
	outcome := ReviewPass
	for _, v := range verdicts {
		switch v.Verdict {
		case VerdictFail:
			return ReviewFail
		case VerdictCannotJudge:
			outcome = ReviewCannotJudge
		}
	}
	return outcome
}

// CalibrationDecision is what a member did with work a review had judged.
type CalibrationDecision string

// The decisions that count (ADR-0019): accepting the work, and sending it back.
const (
	DecisionAccept CalibrationDecision = "accept"
	DecisionRework CalibrationDecision = "rework"
)

// Agreement says whether a member's decision followed the review's outcome.
type Agreement string

// The agreements. A review that could not judge gave the member nothing to agree or disagree with.
const (
	AgreementAgree    Agreement = "agree"
	AgreementDisagree Agreement = "disagree"
	AgreementUnknown  Agreement = "unknown"
)

// AgreementOf compares a member's decision with a review's outcome. Accepting after a pass, or sending
// back after a fail, is agreement. The opposite is disagreement, and a review that could not judge is
// neither.
func AgreementOf(outcome ReviewOutcome, decision CalibrationDecision) Agreement {
	switch {
	case outcome == ReviewPass && decision == DecisionAccept, outcome == ReviewFail && decision == DecisionRework:
		return AgreementAgree
	case outcome == ReviewPass && decision == DecisionRework, outcome == ReviewFail && decision == DecisionAccept:
		return AgreementDisagree
	}
	return AgreementUnknown
}

// CalibrationSource says what made a calibration record.
type CalibrationSource string

// The sources of a record.
const (
	// SourceDecision is a member accepting or sending back work in the acceptance step.
	SourceDecision CalibrationSource = "decision"
	// SourceLaterDefect is work a review passed and a member accepted being sent back after it was
	// done, because a defect turned up in it.
	SourceLaterDefect CalibrationSource = "later_defect"
)

// SeenVerdict is what the review said of one criterion, kept with the record so it reads on its own.
type SeenVerdict struct {
	Criterion string      `json:"criterion"`
	Verdict   VerdictKind `json:"verdict"`
}

// Calibration is the evidence of one member's decision on work a review judged (FR-CAL-1). It is bound to
// the review and to the commit it read, and it holds the merge authority in force when the member decided,
// so a record is read at the level it was made at. Nothing here ever changes that level (FR-CAL-3).
type Calibration struct {
	ID        string `json:"id"`
	WorkOrder string `json:"work_order_id"`
	Review    string `json:"review_id"`
	Commit    string `json:"commit"`
	// Repository is the repository of the work, empty when the work has none.
	Repository string                `json:"repository_id,omitempty"`
	Authority  ledger.MergeAuthority `json:"merge_authority"`
	Outcome    ReviewOutcome         `json:"outcome"`
	Verdicts   []SeenVerdict         `json:"verdicts"`
	Decision   CalibrationDecision   `json:"decision"`
	Agreement  Agreement             `json:"agreement"`
	Source     CalibrationSource     `json:"source"`
	// Member is who decided, and Reason is what they said when they said why.
	Member    string `json:"member,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// OfFirstReview keeps the verdicts of the review the first verdict names. The verdicts that count for one
// attempt come from one review, and a calibration is about that review.
func OfFirstReview(verdicts []Verdict) []Verdict {
	if len(verdicts) == 0 {
		return nil
	}
	var out []Verdict
	for _, v := range verdicts {
		if v.Review == verdicts[0].Review {
			out = append(out, v)
		}
	}
	return out
}

// NewCalibration reads a record from the verdicts a member decided after.
func NewCalibration(id string, w WorkOrder, verdicts []Verdict, authority ledger.MergeAuthority, decision CalibrationDecision, source CalibrationSource, member, reason string) (Calibration, error) {
	seen := OfFirstReview(verdicts)
	if len(seen) == 0 {
		return Calibration{}, fmt.Errorf("%w: a calibration rests on a review's verdicts", ErrInvalid)
	}
	c := Calibration{
		ID: id, WorkOrder: w.ID, Review: seen[0].Review, Commit: seen[0].Commit, Repository: w.RepositoryID, Authority: authority,
		Outcome: ReviewOutcomeOf(seen), Decision: decision, Source: source, Member: member, Reason: strings.TrimSpace(reason),
	}
	c.Agreement = AgreementOf(c.Outcome, decision)
	for _, v := range seen {
		c.Verdicts = append(c.Verdicts, SeenVerdict{Criterion: v.Criterion, Verdict: v.Verdict})
	}
	return c, c.Validate()
}

// Validate refuses a record that is not whole, or whose agreement does not follow from what it holds.
func (c Calibration) Validate() error {
	if c.WorkOrder == "" || c.Review == "" || c.Commit == "" {
		return fmt.Errorf("%w: a calibration names the work, the review, and the commit it judged", ErrInvalid)
	}
	switch c.Outcome {
	case ReviewPass, ReviewFail, ReviewCannotJudge:
	default:
		return fmt.Errorf("%w: calibration outcome %q is not pass, fail, or cannot_judge", ErrInvalid, c.Outcome)
	}
	switch c.Decision {
	case DecisionAccept, DecisionRework:
	default:
		return fmt.Errorf("%w: calibration decision %q is not accept or rework", ErrInvalid, c.Decision)
	}
	switch c.Source {
	case SourceDecision:
	case SourceLaterDefect:
		if c.Outcome != ReviewPass || c.Decision != DecisionRework {
			return fmt.Errorf("%w: a later defect is a rework of work the review passed", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: calibration source %q", ErrInvalid, c.Source)
	}
	if want := AgreementOf(c.Outcome, c.Decision); c.Agreement != want {
		return fmt.Errorf("%w: a member who chose %s after a %s agrees as %q, not %q", ErrInvalid, c.Decision, c.Outcome, want, c.Agreement)
	}
	return nil
}
