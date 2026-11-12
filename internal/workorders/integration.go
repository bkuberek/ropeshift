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
	// request's head moved past the reviewed commit, or the pull request closed.
	StepHold Step = "hold"
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
}

// MergeGate decides one merge, as a pure function of what the ledger holds: ropeshift merges only the reviewed
// commit, only once an approval binds to it, and with review target `pull_request` only while the pull
// request's head is that commit and every check the base requires is green on it (FR-MERGE-5). A required
// check that failed sends the work back and one still running waits. With review target `branch` no forge
// runs checks, so the verdicts, and any check result on record for the attempt, are the gate; a failed one
// never reaches here, because acceptance sends it back.
func MergeGate(in GateInput) Gate {
	pr := in.PullRequest
	if in.Target == ledger.ReviewOnPullRequest && pr != nil && pr.State == ledger.PullRequestMerged {
		return Gate{Step: StepMerged}
	}
	if !in.Approved {
		return Gate{Step: StepWait, Why: fmt.Sprintf("a member has to approve the merge of %s at %s", short(in.Commit), in.Authority)}
	}
	if in.Target != ledger.ReviewOnPullRequest {
		return Gate{Step: StepMerge}
	}
	switch {
	case pr == nil:
		return Gate{Step: StepWait, Why: "the pull request is not on record yet"}
	case pr.State == ledger.PullRequestClosed:
		return Gate{Step: StepHold, Why: fmt.Sprintf("pull request #%d is closed without a merge", pr.Number)}
	case pr.HeadCommit != in.Commit:
		return Gate{Step: StepHold, Why: fmt.Sprintf("pull request #%d's head is %s, not the reviewed commit %s, and ropeshift merges only the reviewed commit", pr.Number, short(pr.HeadCommit), short(in.Commit))}
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
	if len(failed) != 0 {
		return Gate{Step: StepRework, Failed: failed, Why: fmt.Sprintf("required checks failed on %s: %s", short(in.Commit), strings.Join(failed, ", "))}
	}
	if len(pending) != 0 {
		return Gate{Step: StepWait, Why: fmt.Sprintf("required checks are still running on %s: %s", short(in.Commit), strings.Join(pending, ", "))}
	}
	return Gate{Step: StepMerge}
}

// short names a commit by its first eight characters, which is how a person reads one.
func short(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
