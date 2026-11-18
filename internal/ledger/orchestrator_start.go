package ledger

// ScheduledStart is a start of a worker's orchestrator that a member asked for at its sign-in's usage reset, which the
// installation keeps across a restart. StartsAt is the reset it waits for, in epoch milliseconds, and zero while it
// waits for the next reported reset: none was reported, or the one it waited for passed with the limit still holding,
// which Missed counts.
type ScheduledStart struct {
	Worker      string `json:"worker_id"`
	StartsAt    int64  `json:"starts_at,omitempty"`
	ScheduledBy string `json:"scheduled_by"`
	ScheduledAt int64  `json:"scheduled_at"`
	Missed      int64  `json:"missed,omitempty"`
}

// The events of a scheduled start, about its orchestrator.
const (
	// EventOrchestratorStartScheduled records a scheduled start as it stands: as a member asked for it, or moved on
	// to the next reported reset.
	EventOrchestratorStartScheduled = "orchestrator.start_scheduled"
	// EventOrchestratorStartEnded records a scheduled start that ended: a member canceled it, it started the
	// orchestrator, or the orchestrator already ran.
	EventOrchestratorStartEnded = "orchestrator.start_ended"
)
