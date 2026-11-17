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

// WaitingForRunWindow is what holds the run of an attended worker's orchestrator while no run window is
// open. Its worker starts the session only when there is something to judge, and new work is not, until a
// member opens a window, so the run reads `provisioning` until then, or until a question or a message
// wakes it.
const WaitingForRunWindow = "run_window"

// Orchestrator is a worker's orchestrator as a client reads it: the ledger's record, and WaitingFor, what
// holds a run a member wants before its session starts, which the control plane reads at the time of the
// read and no event holds.
type Orchestrator struct {
	ledger.Orchestrator
	WaitingFor string `json:"waiting_for,omitempty"`
}

// OrchestratorState says how an orchestrator reads, in the words every client prints: what holds a run
// whose session has not started, or else the state its worker last said, which is empty until it says one.
func OrchestratorState(o Orchestrator) string {
	if o.WaitingFor == WaitingForRunWindow {
		return "waiting for a run window"
	}
	return o.Actual
}

// Orchestrator reads a worker's orchestrator.
func (c *Client) Orchestrator(ctx context.Context, worker string) (Orchestrator, error) {
	var out Orchestrator
	err := c.Do(ctx, http.MethodGet, workerPath(worker, "orchestrator"), nil, &out)
	return out, err
}

// SetOrchestrator records what a member asks of a worker's orchestrator.
func (c *Client) SetOrchestrator(ctx context.Context, worker string, in SetOrchestrator) (ledger.Orchestrator, error) {
	var out ledger.Orchestrator
	err := c.Do(ctx, http.MethodPut, workerPath(worker, "orchestrator"), in, &out)
	return out, err
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
