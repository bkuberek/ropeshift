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
