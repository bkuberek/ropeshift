package workorders

import (
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
)

// Outcome is how a lane ended (FR-LANE-2).
type Outcome string

// The outcomes.
const (
	OutcomeCompleted Outcome = "completed"
	OutcomeBlocked   Outcome = "blocked"
	OutcomeFailed    Outcome = "failed"
)

// The reasons the worker records when a lane did not report for itself.
const (
	ReasonNoOutcome       = "no_outcome"
	ReasonCrashed         = "crashed"
	ReasonSpawnFailed     = "spawn_failed"
	ReasonNoRepository    = "no_repository"
	ReasonWorkerRestarted = "worker_restarted"
	ReasonFenced          = "fenced"
	// ReasonCanceled says a member canceled the work this lane ran. The ledger records it, because a
	// worker hears only that its lease went.
	ReasonCanceled = "canceled"
	// ReasonUnknownScreen says a checked start stopped on a screen the driver does not
	// know. It stands for the human_resume wake until lanes can park.
	ReasonUnknownScreen = "unknown_screen"
	// ReasonSpendLimit says a generation found its work order at its hard spend limit as it was about to start, so it parked
	// with no process and no model call, which stands for the park until the lane's end is read.
	ReasonSpendLimit = "spend_limit"
	// ReasonSettingsChanged says a generation found its worktree had changed the project settings its agent CLI reads in a
	// key that runs code or widens access, as it was about to start, so it parked with no process for a member's choice;
	// a member who stops the lane blocks its work with this reason (ADR-0040).
	ReasonSettingsChanged = "settings_changed"
	// ReasonHookFailed says a profile's hook did not finish, so no session started.
	ReasonHookFailed = "hook_failed"
	// ReasonHookUnknown says a crash left a hook's end unknown, and a person has to look.
	ReasonHookUnknown = "hook_unknown"
	// ReasonNoInteractiveMode says a planning lane did not start because this machine cannot run it
	// where a person can attach, such as one with no tmux, and it never falls back to headless.
	ReasonNoInteractiveMode = "no_interactive_mode"
	// ReasonDelegatePolicy says a lane did not start because the delegate policy its claim recorded keeps the
	// delegate that sent its own profile layer from raising the model or the effort it would run (FR-AUTH-16).
	// The report's detail names the version and what the layer raised.
	ReasonDelegatePolicy = "delegate_policy"
	// ReasonWorktreeInsideClone says a lane did not start because its worktree root is a clone the worker keeps
	// or lies inside one (FR-PROF-3). The report's detail names the root, the clone, and the layer that set the
	// root, and says how to fix it.
	ReasonWorktreeInsideClone = "worktree_inside_clone"
	// ReasonLayoutChanged says an adopt lane did not start because the layout around the repository is not the one
	// its plan was read from (FR-ADOPT-14): the clones, their remotes, or the files the plan lists differ, or the
	// layout could not be read again. The report's detail names what differs, and the fix is to run
	// `ropeshift adopt --restructure` again.
	ReasonLayoutChanged = "layout_changed"
	// ReasonRestructureIncomplete says an adopt lane did not start because the restructuring its work kept has no
	// root or no digests of the plan's parts, as every restructuring kept before the plan's request was, so the
	// layout cannot be read again as the plan read it. The layout did not change. The report's detail names what
	// the restructuring lacks, and the fix is to run `ropeshift adopt --dry-run` again and restart with
	// `--restructure`.
	ReasonRestructureIncomplete = "restructure_incomplete"
	// ReasonBackgroundUnreported says a lane rested on background work its own turn left running past the longest a
	// lane waits live (max_live_wait), after its nudge, and nothing reported. The report's detail says since when, how
	// many tasks still ran, and the bound that ended the wait.
	ReasonBackgroundUnreported = "background_unreported"
)

// CheckResult is one check a lane reports. Criterion names what it speaks for, and a check
// that names none covers no criterion.
type CheckResult struct {
	Name      string `json:"name"`
	Passed    bool   `json:"passed"`
	Criterion string `json:"criterion,omitempty"`
}

// Report is a lane's outcome, with its evidence.
type Report struct {
	Outcome Outcome `json:"outcome"`
	Reason  string  `json:"reason,omitempty"`
	// Detail is what the worker says of a reason it recorded, such as the delegate policy version that refused
	// a lane's start. Empty says nothing more than the reason.
	Detail    string        `json:"detail,omitempty"`
	Artifacts []string      `json:"artifacts,omitempty"`
	Checks    []CheckResult `json:"checks,omitempty"`
	// Verdicts are a review lane's judgment, one per criterion of the contract it read. Another lane
	// reports none.
	Verdicts []CriterionVerdict `json:"verdicts,omitempty"`
	// Notes are the security concerns a review lane found in the diff, whatever its criteria say. Another lane
	// reports none.
	Notes []ReviewNote `json:"notes,omitempty"`
}

// Validate refuses a report that names no outcome the ledger holds, or whose verdicts or notes are not whole.
func (r Report) Validate() error {
	switch r.Outcome {
	case OutcomeCompleted, OutcomeBlocked, OutcomeFailed:
		if err := r.checkVerdicts(); err != nil {
			return err
		}
		return r.checkNotes()
	}
	return fmt.Errorf("%w: outcome %q", ErrInvalid, r.Outcome)
}

// Ending is what an outcome does to a work order.
type Ending struct {
	Trigger  Trigger
	To       State
	Attempts int64
}

// EndOf follows the outcome table. A failed lane leaves its work `failed` whether or not attempts
// remain, and the trigger says which: a retry is judged by an orchestrator or made by a member, so a
// crash that repeats does not spend every attempt unseen (ADR-0014). byDefault is the attempts a work
// order gets when it names no bound, which the recovery policy in force states.
func EndOf(w WorkOrder, outcome Outcome, byDefault int64) (Ending, error) {
	switch outcome {
	case OutcomeCompleted:
		return Ending{TriggerLaneCompleted, Acceptance, w.Attempts}, nil
	case OutcomeBlocked:
		return Ending{TriggerLaneBlocked, Blocked, w.Attempts}, nil
	case OutcomeFailed:
		attempts := w.Attempts + 1
		if attempts < w.AttemptBound(byDefault) {
			return Ending{TriggerLaneFailed, Failed, attempts}, nil
		}
		return Ending{TriggerAttemptsExhausted, Failed, attempts}, nil
	}
	return Ending{}, fmt.Errorf("%w: outcome %q", ErrInvalid, outcome)
}

// LaneResult is what a lane's end records: its report, and what only the worker saw.
// An agent's report cannot set the cost or the branch, because each is a separate value.
type LaneResult struct {
	Report
	// Branch and Head are the final artifact (D17): the branch a member merges, and its commit.
	Branch string `json:"branch,omitempty"`
	Head   string `json:"head,omitempty"`
	// Change is what the branch changed since the lane's base commit, which the worker measured. It is
	// absent when the lane had no base commit or made no commit.
	Change *ledger.ChangeSize `json:"change,omitempty"`
	// ImportMerge is the merge commit the worker made on the branch to import a design repository's history before
	// the lane began (FR-ADOPT-13). It is empty for a lane whose worker imported nothing.
	ImportMerge string `json:"import_merge,omitempty"`
	// MaxReviewRounds is the bound on review rounds the profile of the lane that ended named, which a
	// review's verdicts are settled against. Zero names none, and the default holds.
	MaxReviewRounds int64   `json:"max_review_rounds,omitempty"`
	CostUSD         float64 `json:"cost_usd,omitempty"`
	SessionTotalUSD float64 `json:"session_total_cost_usd,omitempty"`
	// UnknownCostTurns counts the turns of every generation whose cost was never reported: each turn that stopped,
	// and the turn a session crashed in or ended in with an input unanswered. Any one makes CostUSD a lower bound.
	UnknownCostTurns int64 `json:"unknown_cost_turns,omitempty"`
	InputTokens      int64 `json:"input_tokens,omitempty"`
	OutputTokens     int64 `json:"output_tokens,omitempty"`
	// Settings are what the lane ran on, for the experience record of a lane that never bound.
	Settings *measures.Settings `json:"settings,omitempty"`
}

// AttemptsLeft says a failed work order may run again without a member adding an attempt, where byDefault
// is the attempts a work order that names no bound gets.
func (w WorkOrder) AttemptsLeft(byDefault int64) bool { return w.Attempts < w.AttemptBound(byDefault) }

// AttemptBound is how many attempts a work order allows: its own bound, or byDefault when it names none,
// and one when neither names any.
func (w WorkOrder) AttemptBound(byDefault int64) int64 {
	if w.MaxAttempts > 0 {
		return w.MaxAttempts
	}
	return max(byDefault, 1)
}
