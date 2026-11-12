package workorders

import (
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
)

// Step is what the integration rule says comes next for work in `integrating` (ADR-0020).
type Step string

// The steps of the integration rule.
const (
	// StepWait waits for something the ledger will hear of: an approval, a required check, a pull request.
	StepWait Step = "wait"
	// StepMerge asks a worker to merge the reviewed commit.
	StepMerge Step = "merge"
	// StepRework sends the work back, because a required check failed on the reviewed commit (FR-MERGE-2).
	StepRework Step = "rework"
	// StepHold waits for a member, because nothing ropeshift may do makes the merge possible: the pull
	// request closed with no merge.
	StepHold Step = "hold"
	// StepReview sends the work back to review at the pull request's new head, because the head moved past
	// the reviewed commit: an approval binds to the commit it approved, and the new head was never judged.
	StepReview Step = "review"
	// StepMerged ends the work: its pull request merged on the forge, whoever merged it (FR-MERGE-9).
	StepMerged Step = "merged"
)

// GateInput is what the integration rule reads of one work order in `integrating`.
type GateInput struct {
	Authority ledger.MergeAuthority
	Target    ledger.ReviewTarget
	// Commit is the reviewed commit, the one an approval binds to and the only one ropeshift merges.
	Commit string
	// Approved says an approval binds to Commit: the review's every-criterion pass at review_approves, or a
	// member's at any level.
	Approved bool
	// PullRequest is the pull request the work lands through, as a worker last read it, with review target
	// `pull_request`. Its checks count only when its head is Commit.
	PullRequest *ledger.PullRequest
}

// Gate is the integration rule's answer, with what a member reads of a wait or a hold, and the required
// checks a rework carries.
type Gate struct {
	Step   Step
	Why    string
	Failed []string
	// Head is the pull request's new head, which StepReview reviews.
	Head string
}

// MergeGate decides one merge, as a pure function of what the ledger holds: ropeshift merges only the
// reviewed commit, only once an approval binds to it, and with review target `pull_request` only while the pull
// request's head is that commit and every check the base requires is green on it (FR-MERGE-5). A head that
// moved goes back to review at the new head (ADR-0020). A required check that failed sends the work back and
// one still running waits. At person_merges ropeshift never merges: the work waits for the person's merge on
// the forge, which ends it. With review target `branch` no forge runs checks, so the verdicts, and any check
// result on record for the attempt, are the gate; a failed one never reaches here, because acceptance sends it
// back.
func MergeGate(in GateInput) Gate {
	if in.Target == ledger.ReviewOnPullRequest {
		if gate, decided := pullRequestGate(in); decided {
			return gate
		}
	}
	if !in.Approved {
		return Gate{Step: StepWait, Why: fmt.Sprintf("a member has to approve the merge of %s at %s", short(in.Commit), in.Authority)}
	}
	return Gate{Step: StepMerge}
}

// pullRequestGate reads the pull request, and decides whatever it decides before an approval counts.
func pullRequestGate(in GateInput) (Gate, bool) {
	pr := in.PullRequest
	switch {
	case pr == nil:
		return Gate{Step: StepWait, Why: "the pull request is not on record yet"}, true
	case pr.State == ledger.PullRequestMerged:
		return Gate{Step: StepMerged}, true
	case pr.State == ledger.PullRequestClosed && in.Authority == ledger.PersonMerges:
		return Gate{Step: StepWait, Why: fmt.Sprintf("pull request #%d is closed without a merge; reopen and merge it, or send the work back", pr.Number)}, true
	case pr.State == ledger.PullRequestClosed:
		return Gate{Step: StepHold, Why: fmt.Sprintf("pull request #%d is closed without a merge", pr.Number)}, true
	case pr.HeadCommit != in.Commit:
		return Gate{Step: StepReview, Head: pr.HeadCommit, Why: fmt.Sprintf("pull request #%d's head moved from the reviewed commit %s to %s, which is reviewed again", pr.Number, short(in.Commit), short(pr.HeadCommit))}, true
	}
	var failed, pending []string
	for _, check := range pr.Checks {
		switch {
		case !check.Required:
		case check.State == ledger.CheckFailed:
			failed = append(failed, check.Name)
		case check.State == ledger.CheckPending:
			pending = append(pending, check.Name)
		}
	}
	switch {
	case len(failed) != 0:
		return Gate{Step: StepRework, Failed: failed, Why: fmt.Sprintf("required checks failed on %s: %s", short(in.Commit), strings.Join(failed, ", "))}, true
	case len(pending) != 0:
		return Gate{Step: StepWait, Why: fmt.Sprintf("required checks are still running on %s: %s", short(in.Commit), strings.Join(pending, ", "))}, true
	case in.Authority == ledger.PersonMerges:
		return Gate{Step: StepWait, Why: fmt.Sprintf("at person_merges you merge pull request #%d yourself, and the work ends when ropeshift reads your merge", pr.Number)}, true
	}
	return Gate{}, false
}

// short names a commit by its first eight characters, which is how a person reads one.
func short(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
