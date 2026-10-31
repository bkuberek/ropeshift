package workorders

import "fmt"

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
)

// CheckResult is one check a lane reports.
type CheckResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

// Report is a lane's outcome, with its evidence.
type Report struct {
	Outcome   Outcome       `json:"outcome"`
	Reason    string        `json:"reason,omitempty"`
	Artifacts []string      `json:"artifacts,omitempty"`
	Checks    []CheckResult `json:"checks,omitempty"`
}

// Validate refuses a report that names no outcome the ledger holds.
func (r Report) Validate() error {
	switch r.Outcome {
	case OutcomeCompleted, OutcomeBlocked, OutcomeFailed:
		return nil
	}
	return fmt.Errorf("%w: outcome %q", ErrInvalid, r.Outcome)
}

// Ending is what an outcome does to a work order.
type Ending struct {
	Trigger  Trigger
	To       State
	Attempts int64
}

// EndOf follows the outcome table. A failed lane returns to `ready` while attempts
// remain, and a work order with no attempts left fails.
func EndOf(w WorkOrder, outcome Outcome) (Ending, error) {
	switch outcome {
	case OutcomeCompleted:
		return Ending{TriggerLaneCompleted, Acceptance, w.Attempts}, nil
	case OutcomeBlocked:
		return Ending{TriggerLaneBlocked, Blocked, w.Attempts}, nil
	case OutcomeFailed:
		attempts := w.Attempts + 1
		if attempts < w.MaxAttempts {
			return Ending{TriggerLaneFailedRetry, Ready, attempts}, nil
		}
		return Ending{TriggerAttemptsExhausted, Failed, attempts}, nil
	}
	return Ending{}, fmt.Errorf("%w: outcome %q", ErrInvalid, outcome)
}

// LaneResult is what a lane's end records: its report, and what only the worker saw.
// An agent's report cannot set the cost, because it is a separate value.
type LaneResult struct {
	Report
	CostUSD          float64 `json:"cost_usd,omitempty"`
	SessionTotalUSD  float64 `json:"session_total_cost_usd,omitempty"`
	UnknownCostTurns int64   `json:"unknown_cost_turns,omitempty"`
	InputTokens      int64   `json:"input_tokens,omitempty"`
	OutputTokens     int64   `json:"output_tokens,omitempty"`
}
