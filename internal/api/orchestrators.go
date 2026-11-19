package api

import (
	"context"
	"net/http"

	"ropeshift.com/ropeshift/internal/ledger"
)

// SetOrchestrator asks for a worker's orchestrator to run or stop.
type SetOrchestrator struct {
	Desired string `json:"desired_state"`
}

// What holds the run of an orchestrator a member wants before its session starts. Its worker starts the
// session only when there is something to judge, and until then says the run reads `awaiting_wake`.
const (
	// WaitingForRunWindow is that wait on an attended worker with no run window open, where new work is
	// nothing to judge until a member opens a window, or until a question or a message wakes the run.
	WaitingForRunWindow = "run_window"
	// WaitingForWork is that wait on any other worker.
	WaitingForWork = "work"
)

// Orchestrator is a worker's orchestrator as a client reads it: the ledger's record, and WaitingFor, what
// holds a run a member wants before its session starts, which the control plane reads at the time of the
// read and no event holds.
type Orchestrator struct {
	ledger.Orchestrator
	WaitingFor string `json:"waiting_for,omitempty"`
	// Hold is what keeps it from starting or running now, and Scheduled the start a member scheduled for its
	// sign-in's usage reset; each is absent when there is none.
	Hold      *ledger.OrchestratorHold `json:"hold,omitempty"`
	Scheduled *ledger.ScheduledStart   `json:"scheduled,omitempty"`
}

// OrchestratorState says how an orchestrator reads, in the words every client prints: what holds a run
// whose session has not started, or else the state its worker last said, which is empty until it says one.
func OrchestratorState(o Orchestrator) string {
	switch o.WaitingFor {
	case WaitingForRunWindow:
		return "waiting for a run window"
	case WaitingForWork:
		return "waiting for work"
	}
	return o.Actual
}

// Orchestrator reads a worker's orchestrator.
func (c *Client) Orchestrator(ctx context.Context, worker string) (Orchestrator, error) {
	var out Orchestrator
	err := c.Do(ctx, http.MethodGet, workerPath(worker, "orchestrator"), nil, &out)
	return out, err
}

// SetOrchestrator records what a member asks of a worker's orchestrator, and reads it back as Orchestrator does.
func (c *Client) SetOrchestrator(ctx context.Context, worker string, in SetOrchestrator) (Orchestrator, error) {
	var out Orchestrator
	err := c.Do(ctx, http.MethodPut, workerPath(worker, "orchestrator"), in, &out)
	return out, err
}

// ScheduleOrchestrator starts a worker's orchestrator at its sign-in's usage reset, which a member asks for while the
// limit holds.
func (c *Client) ScheduleOrchestrator(ctx context.Context, worker string) (ledger.ScheduledStart, error) {
	var out ledger.ScheduledStart
	err := c.Do(ctx, http.MethodPost, workerPath(worker, "orchestrator", "schedule"), struct{}{}, &out)
	return out, err
}

// CancelScheduledStart cancels the start scheduled for a worker's orchestrator.
func (c *Client) CancelScheduledStart(ctx context.Context, worker string) error {
	return c.Do(ctx, http.MethodDelete, workerPath(worker, "orchestrator", "schedule"), nil, nil)
}

// Orchestrate carries one act of a worker's orchestrator.
func (c *Client) Orchestrate(ctx context.Context, verb string, in ledger.OrchestratorCall) (ledger.OrchestratorReply, error) {
	var out ledger.OrchestratorReply
	err := c.Do(ctx, http.MethodPost, workerPath(in.Auth.WorkerID, "orchestrator", "acts", verb), in, &out)
	return out, err
}

// WakeDigest reads what a worker's orchestrator could judge now.
func (c *Client) WakeDigest(ctx context.Context, worker string, in ledger.WakeRequest) (ledger.WakeDigest, error) {
	var out ledger.WakeDigest
	err := c.Do(ctx, http.MethodPost, workerPath(worker, "orchestrator", "digest"), in, &out)
	return out, err
}
