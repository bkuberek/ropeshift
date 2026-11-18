package api

import (
	"context"
	"net/http"
	"net/url"

	"ropeshift.com/ropeshift/internal/ledger"
)

// SpendKey is one setting of a spend policy as `spend set` names it, with its value in force as `spend set` reads it.
type SpendKey struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// SpendPolicyView is a spend policy in force: its version and who published it, and each key with its value. The
// version of a policy nobody published is the built-in one, authored by the built-in author.
type SpendPolicyView struct {
	Version    int64      `json:"version"`
	AuthoredBy string     `json:"authored_by"`
	CreatedAt  int64      `json:"created_at"`
	Keys       []SpendKey `json:"keys"`
}

// SpendView is where a project's spend stands (ADR-0037): the project's spend policy in force, the installation's, and
// the spend of each open work order that a limit covers, charged and labeled, against its limits.
type SpendView struct {
	Project      SpendPolicyView         `json:"project"`
	Installation SpendPolicyView         `json:"installation"`
	WorkOrders   []ledger.WorkOrderSpend `json:"work_orders"`
	// ProjectSpend is what the project's lanes were charged today and this week, against its daily and weekly limits
	// (FR-SPEND-6). A limit of 0 is none.
	ProjectSpend ledger.ProjectSpend `json:"project_spend"`
}

// Spend reads where this project's spend stands.
func (c *ProjectClient) Spend(ctx context.Context) (SpendView, error) {
	var out SpendView
	err := c.do(ctx, http.MethodGet, "/v1/spend", nil, &out)
	return out, err
}

// RaiseSpend asks to raise a work order's hard limit by a percentage of the limit in force, or to an amount in US dollars
// (FR-SPEND-7). It names one of the two.
type RaiseSpend struct {
	ByPercent float64 `json:"by_percent,omitempty"`
	ToUSD     float64 `json:"to_usd,omitempty"`
}

// RaiseSpend raises a work order's hard limit and wakes the lane the limit parked, and answers the limit before and
// after.
func (c *ProjectClient) RaiseSpend(ctx context.Context, id string, in RaiseSpend) (ledger.SpendLimitChange, error) {
	var out ledger.SpendLimitChange
	err := c.do(ctx, http.MethodPost, "/v1/work-orders/"+url.PathEscape(id)+"/spend/raise", in, &out)
	return out, err
}

// RaiseProjectSpend asks to raise the project's daily or weekly limit for the window it is made in, by a percentage of the
// limit in force or to an amount in US dollars (FR-SPEND-6). It names the window, `day` or `week`, and one of the two.
type RaiseProjectSpend struct {
	Window    string  `json:"window"`
	ByPercent float64 `json:"by_percent,omitempty"`
	ToUSD     float64 `json:"to_usd,omitempty"`
	// WindowStart is the start of the window the raise was asked for, in milliseconds since the epoch. A card for a delegate's
	// raise carries it, and a raise for a window that is over is refused, so a confirmation never lifts a later window than
	// the one a member read. Left out, the raise is for the window it is made in.
	WindowStart int64 `json:"window_start,omitempty"`
}

// RaiseProjectSpend raises a project's daily or weekly limit and wakes the lanes the project's limits parked, and answers
// the limit before and after.
func (c *ProjectClient) RaiseProjectSpend(ctx context.Context, in RaiseProjectSpend) (ledger.ProjectLimitChange, error) {
	var out ledger.ProjectLimitChange
	err := c.do(ctx, http.MethodPost, "/v1/spend/project/raise", in, &out)
	return out, err
}

// ResumeSpend grants a work order one more generation's allowance so the lane its hard limit parked goes on, and answers
// the limit before and after.
func (c *ProjectClient) ResumeSpend(ctx context.Context, id string) (ledger.SpendLimitChange, error) {
	var out ledger.SpendLimitChange
	err := c.do(ctx, http.MethodPost, "/v1/work-orders/"+url.PathEscape(id)+"/spend/resume", struct{}{}, &out)
	return out, err
}

// SpendStopEnded is what blocking a spend stop or sending it to replanning left: the work order, the state it is in,
// and the reason it records.
type SpendStopEnded struct {
	WorkOrder string `json:"work_order"`
	State     string `json:"state"`
	Reason    string `json:"reason"`
}

// BlockSpend ends a spend stop by blocking its work order with the reason `spend_limit`.
func (c *ProjectClient) BlockSpend(ctx context.Context, id string) (SpendStopEnded, error) {
	var out SpendStopEnded
	err := c.do(ctx, http.MethodPost, "/v1/work-orders/"+url.PathEscape(id)+"/spend/block", struct{}{}, &out)
	return out, err
}

// ReplanSpend ends a spend stop by sending its work order to planning.
func (c *ProjectClient) ReplanSpend(ctx context.Context, id string) (SpendStopEnded, error) {
	var out SpendStopEnded
	err := c.do(ctx, http.MethodPost, "/v1/work-orders/"+url.PathEscape(id)+"/spend/replan", struct{}{}, &out)
	return out, err
}

// WorkOrderSpend reads one work order's charged spend against its limits.
func (c *ProjectClient) WorkOrderSpend(ctx context.Context, id string) (ledger.WorkOrderSpend, error) {
	var out ledger.WorkOrderSpend
	err := c.do(ctx, http.MethodGet, "/v1/work-orders/"+url.PathEscape(id)+"/spend", nil, &out)
	return out, err
}
