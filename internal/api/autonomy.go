package api

import (
	"context"
	"net/http"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

// SetAutonomy chooses a worker's level.
type SetAutonomy struct {
	Level authority.Level `json:"level"`
}

// AcknowledgeRisk stands behind the statement a digest names, for one worker.
type AcknowledgeRisk struct {
	Digest string `json:"digest"`
}

// OpenWindow opens a run window. The client generates ID, so a retry returns the first answer, and
// a length of zero takes the default.
type OpenWindow struct {
	ID       string `json:"id"`
	LengthMs int64  `json:"length_ms,omitempty"`
}

// RiskStatement is the text a member acknowledges, and the digest that names it.
type RiskStatement struct {
	Statement string `json:"statement"`
	Digest    string `json:"digest"`
}

// RiskPath is where the statement reads.
const RiskPath = "/v1/autonomy/risk"

// Autonomy reads where a worker stands.
func (c *Client) Autonomy(ctx context.Context, worker string) (authority.AutonomyView, error) {
	var out authority.AutonomyView
	err := c.Do(ctx, http.MethodGet, workerPath(worker, "autonomy"), nil, &out)
	return out, err
}

// SetAutonomy records the level a member chose.
func (c *Client) SetAutonomy(ctx context.Context, worker string, in SetAutonomy) error {
	return c.Do(ctx, http.MethodPut, workerPath(worker, "autonomy"), in, nil)
}

// Risk reads the statement a member acknowledges.
func (c *Client) Risk(ctx context.Context) (RiskStatement, error) {
	var out RiskStatement
	err := c.Do(ctx, http.MethodGet, RiskPath, nil, &out)
	return out, err
}

// AcknowledgeRisk records a member's acknowledgement.
func (c *Client) AcknowledgeRisk(ctx context.Context, worker string, in AcknowledgeRisk) error {
	return c.Do(ctx, http.MethodPost, workerPath(worker, "autonomy", "acknowledgement"), in, nil)
}

// OpenWindow opens a run window for one worker.
func (c *Client) OpenWindow(ctx context.Context, worker string, in OpenWindow) (authority.RunWindow, error) {
	var out authority.RunWindow
	err := c.Do(ctx, http.MethodPost, workerPath(worker, "windows"), in, &out)
	return out, err
}

// CloseWindow ends the window that stands for one worker.
func (c *Client) CloseWindow(ctx context.Context, worker string) error {
	return c.Do(ctx, http.MethodDelete, workerPath(worker, "windows"), nil, nil)
}

// SetLaneCaps sets a worker's lane caps. MaxLanes null, or left out, follows the worker's recommendation,
// and zero caps nothing; MaxLanesPerSignIn zero caps nothing. Neither has an upper bound.
type SetLaneCaps struct {
	MaxLanes          *int64 `json:"max_lanes"`
	MaxLanesPerSignIn int64  `json:"max_lanes_per_sign_in"`
}

// LaneCaps reads a worker's lane caps, with what is in force and its recommendation.
func (c *Client) LaneCaps(ctx context.Context, worker string) (ledger.WorkerCaps, error) {
	var out ledger.WorkerCaps
	err := c.Do(ctx, http.MethodGet, workerPath(worker, "caps"), nil, &out)
	return out, err
}

// SetLaneCaps writes a worker's lane caps, and the answer warns when the per-worker cap is above the
// recommendation.
func (c *Client) SetLaneCaps(ctx context.Context, worker string, in SetLaneCaps) (ledger.WorkerCaps, error) {
	var out ledger.WorkerCaps
	err := c.Do(ctx, http.MethodPut, workerPath(worker, "caps"), in, &out)
	return out, err
}
