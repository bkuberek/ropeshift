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

// ResumeSpend grants a work order one more generation's allowance so the lane its hard limit parked goes on, and answers
// the limit before and after.
func (c *ProjectClient) ResumeSpend(ctx context.Context, id string) (ledger.SpendLimitChange, error) {
	var out ledger.SpendLimitChange
	err := c.do(ctx, http.MethodPost, "/v1/work-orders/"+url.PathEscape(id)+"/spend/resume", struct{}{}, &out)
	return out, err
}

// WorkOrderSpend reads one work order's charged spend against its limits.
func (c *ProjectClient) WorkOrderSpend(ctx context.Context, id string) (ledger.WorkOrderSpend, error) {
	var out ledger.WorkOrderSpend
	err := c.do(ctx, http.MethodGet, "/v1/work-orders/"+url.PathEscape(id)+"/spend", nil, &out)
	return out, err
}
