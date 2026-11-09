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

// Orchestrator reads a worker's orchestrator.
func (c *Client) Orchestrator(ctx context.Context, worker string) (ledger.Orchestrator, error) {
	var out ledger.Orchestrator
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
