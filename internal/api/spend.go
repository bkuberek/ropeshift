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

// WorkOrderSpend reads one work order's charged spend against its limits.
func (c *ProjectClient) WorkOrderSpend(ctx context.Context, id string) (ledger.WorkOrderSpend, error) {
	var out ledger.WorkOrderSpend
	err := c.do(ctx, http.MethodGet, "/v1/work-orders/"+url.PathEscape(id)+"/spend", nil, &out)
	return out, err
}
