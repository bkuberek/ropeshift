package workorders

import (
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
)

// VerdictKind is what a review found of one criterion.
type VerdictKind string

// The verdicts a review gives (FR-REVIEW-4).
const (
	VerdictPass        VerdictKind = "pass"
	VerdictFail        VerdictKind = "fail"
	VerdictCannotJudge VerdictKind = "cannot_judge"
)

// DefaultMaxReviewRounds bounds the reviews of one work order when no profile says otherwise
// (FR-REVIEW-8).
const DefaultMaxReviewRounds int64 = 3

// CriterionVerdict is one criterion's verdict as a review lane reports it: what it found, why, and what
// it rests on. A citation is a file and line, a requirement ID, or a document as kind:id.
type CriterionVerdict struct {
	Criterion string      `json:"criterion"`
	Verdict   VerdictKind `json:"verdict"`
	Reasons   string      `json:"reasons"`
	Citations []string    `json:"citations,omitempty"`
}

// Validate refuses a verdict that is not whole. Every verdict says why, because a bare pass or fail gives
// the next lane and the person nothing to act on.
func (v CriterionVerdict) Validate() error {
	switch v.Verdict {
	case VerdictPass, VerdictFail, VerdictCannotJudge:
	default:
		return fmt.Errorf("%w: verdict %q for criterion %q is not pass, fail, or cannot_judge", ErrInvalid, v.Verdict, v.Criterion)
	}
	if strings.TrimSpace(v.Criterion) == "" {
		return fmt.Errorf("%w: a verdict names no criterion", ErrInvalid)
	}
	if strings.TrimSpace(v.Reasons) == "" {
		return fmt.Errorf("%w: the verdict for criterion %q gives no reasons", ErrInvalid, v.Criterion)
	}
	for _, citation := range v.Citations {
		if strings.TrimSpace(citation) == "" {
			return fmt.Errorf("%w: the verdict for criterion %q has an empty citation", ErrInvalid, v.Criterion)
		}
	}
	return nil
}

// checkVerdicts refuses a report whose verdicts are not whole, or that judge a criterion twice.
func (r Report) checkVerdicts() error {
	seen := map[string]bool{}
	for _, v := range r.Verdicts {
		if err := v.Validate(); err != nil {
			return err
		}
		if seen[v.Criterion] {
			return fmt.Errorf("%w: criterion %q is judged twice", ErrInvalid, v.Criterion)
		}
		seen[v.Criterion] = true
	}
	return nil
}

// CheckVerdicts holds a completed review's report to its contract: one verdict for each criterion and no
// verdict for any other, because a review that skips a criterion has not judged the work (FR-REVIEW-4).
func (r Report) CheckVerdicts(criteria []string) error {
	if err := r.checkVerdicts(); err != nil {
		return err
	}
	judged := map[string]bool{}
	for _, v := range r.Verdicts {
		judged[v.Criterion] = true
	}
	var missing, unknown []string
	for _, id := range criteria {
		if !judged[id] {
			missing = append(missing, id)
		}
	}
	for id := range judged {
		if !slices.Contains(criteria, id) {
			unknown = append(unknown, id)
		}
	}
	slices.Sort(unknown)
	var problems []string
	if len(missing) != 0 {
		problems = append(problems, "gives no verdict for criterion "+strings.Join(missing, ", "))
	}
	if len(unknown) != 0 {
		problems = append(problems, "judges criterion "+strings.Join(unknown, ", ")+", which the contract does not have")
	}
	if len(problems) != 0 {
		return fmt.Errorf("%w: the review %s", ErrInvalid, strings.Join(problems, ", and "))
	}
	return nil
}

// Verdict is a criterion's verdict on record. It is bound to the commit the review read and to the attempt
// that left it (FR-REVIEW-4): a branch that moved, or a later attempt, is not judged by it. Lane and
// Generation name who gave it, and Fence is the review's own fence when it ended.
type Verdict struct {
	CriterionVerdict
	Review     string `json:"review_id"`
	WorkOrder  string `json:"work_order_id"`
	Attempt    int64  `json:"attempt"`
	Commit     string `json:"commit"`
	Lane       string `json:"lane_id"`
	Generation int64  `json:"generation"`
	Fence      int64  `json:"fence"`
	// MaxRounds is the bound on review rounds the review lane's profile named when it ended, and zero
	// when it named none.
	MaxRounds int64 `json:"max_rounds,omitempty"`
	CreatedAt int64 `json:"created_at"`
}

// Counted keeps the verdicts that speak for the attempt a work order is at and the commit it left. A
// verdict on any other commit or attempt stays on record and decides nothing.
func Counted(verdicts []Verdict, attempt int64, commit string) []Verdict {
	var out []Verdict
	if commit == "" {
		return out
	}
	for _, v := range verdicts {
		if v.Attempt == attempt && v.Commit == commit {
			out = append(out, v)
		}
	}
	return out
}

// Rounds counts the reviews that gave verdicts on a work order, which is how many times it has been judged.
func Rounds(verdicts []Verdict) int64 {
	reviews := map[string]bool{}
	for _, v := range verdicts {
		reviews[v.Review] = true
	}
	return int64(len(reviews))
}

// ReviewLine is one review of a work order, for a reader who asks how it was judged.
type ReviewLine struct {
	ID    string `json:"id"`
	State State  `json:"state"`
	// Attempt is the fence of the attempt it judges.
	Attempt int64 `json:"attempt"`
}

// ReworkNote is why completed work went back for another attempt: a member's reason, or the review whose
// failed criteria sent it. The next lane's brief carries it.
type ReworkNote struct {
	WorkOrder string `json:"work_order_id"`
	// Attempt is the fence the work came back from.
	Attempt int64 `json:"attempt"`
	// Review is the review work order whose verdicts sent it back. Empty for a member's own reason.
	Review    string `json:"review_id,omitempty"`
	Reason    string `json:"reason"`
	CreatedAt int64  `json:"created_at"`
}

// Settlement is what an attempt's verdicts and evidence settle without anyone's word added.
type Settlement string

// The settlements.
const (
	// SettleNone means the verdicts decide nothing, because there are none for this attempt.
	SettleNone Settlement = ""
	// SettleAccept means the review approves and the repository's merge authority leaves that to it.
	SettleAccept Settlement = "accept"
	// SettleRework means a criterion failed, or a check did, and another attempt has rounds left.
	SettleRework Settlement = "rework"
	// SettleMember means the work waits for a member, who reads the verdicts first.
	SettleMember Settlement = "member"
)

// JudgeInput is what the verdicts of one attempt are read against.
type JudgeInput struct {
	// Criteria are the pinned contract's criteria.
	Criteria []string
	// Verdicts are the ones that count for this attempt and its commit.
	Verdicts []Verdict
	// Assessment is what the attempt's check results say.
	Assessment Assessment
	// Authority is the repository's merge authority, which an unset record reads as the default.
	Authority ledger.MergeAuthority
	// Rounds is how many reviews of this work order gave verdicts, this one included, and MaxRounds bounds
	// them. A bound below one is the default.
	Rounds, MaxRounds int64
}

// Judgement is a settlement with what it rests on.
type Judgement struct {
	Settlement Settlement
	// Failed are the verdicts that failed, for a rework to carry.
	Failed []Verdict
	// Why says what a member's wait is for, when the work waits for one.
	Why string
}

// Judge reads one attempt's verdicts against its checks (ADR-0019). A failed criterion or a failed check
// sends the work back, until the rounds are used up and a person reads it. When nothing failed, work
// whose every criterion passed is accepted only at review_approves, and below that it waits for the
// member whose word the level keeps. A criterion the review could not judge waits for a member unless a
// passing forge check covers it. A lane's own check never covers one, because it is the lane's word.
func Judge(in JudgeInput) Judgement {
	if len(in.Verdicts) == 0 {
		return Judgement{}
	}
	limit := in.MaxRounds
	if limit < 1 {
		limit = DefaultMaxReviewRounds
	}
	var failed []Verdict
	for _, v := range in.Verdicts {
		if v.Verdict == VerdictFail {
			failed = append(failed, v)
		}
	}
	if len(failed) != 0 || len(in.Assessment.Failed) != 0 {
		if in.Rounds >= limit {
			return Judgement{Settlement: SettleMember, Why: fmt.Sprintf("the review has judged this work %d times, and the bound is %d", in.Rounds, limit)}
		}
		return Judgement{Settlement: SettleRework, Failed: failed}
	}
	for _, v := range in.Verdicts {
		if v.Verdict == VerdictCannotJudge && !coveredBy(in.Assessment, v.Criterion) {
			return Judgement{Settlement: SettleMember, Why: "the review could not judge criterion " + v.Criterion}
		}
	}
	if len(in.Criteria) == 0 || in.Authority.PersonDecides() {
		return Judgement{Settlement: SettleMember, Why: "the review passed every criterion, and this repository leaves the decision to you"}
	}
	return Judgement{Settlement: SettleAccept}
}

func coveredBy(assessment Assessment, criterion string) bool {
	for _, row := range assessment.Criteria {
		if row.Criterion == criterion {
			return row.Standing == StandingCovered
		}
	}
	return false
}
