package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"ropeshift.com/ropeshift/internal/authority"
)

// The refusals of a decision.
const (
	CodeSelfApproval  = "self_approval"
	CodeStaleDigest   = "stale_digest"
	CodeNotDelegated  = "not_delegated"
	CodeNotADecider   = "not_a_decider"
	CodeRequestClosed = "request_closed"
)

// ApprovalList is what a list of requests found.
type ApprovalList struct {
	Approvals []authority.Approval `json:"approvals"`
}

// DecideApproval is a member's word on a request. Digest is the one the member read.
type DecideApproval struct {
	Digest string `json:"digest"`
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
}

// Approvals lists the requests in one state, oldest first. An empty state lists them all.
func (c *Client) Approvals(ctx context.Context, state authority.ApprovalState) ([]authority.Approval, error) {
	path := "/v1/approvals"
	if state != "" {
		path += "?state=" + url.QueryEscape(string(state))
	}
	var out ApprovalList
	err := c.Do(ctx, http.MethodGet, path, nil, &out)
	return out.Approvals, err
}

// Approval reads one request.
func (c *Client) Approval(ctx context.Context, id string) (authority.Approval, error) {
	var out authority.Approval
	err := c.Do(ctx, http.MethodGet, "/v1/approvals/"+url.PathEscape(id), nil, &out)
	return out, err
}

// DecideApproval carries a member's word on a request.
func (c *Client) DecideApproval(ctx context.Context, id string, in DecideApproval) (authority.Approval, error) {
	var out authority.Approval
	err := c.Do(ctx, http.MethodPost, "/v1/approvals/"+url.PathEscape(id)+"/decide", in, &out)
	return out, err
}

// UseApproval reads where a call stands, for a lane this worker holds.
func (c *Client) UseApproval(ctx context.Context, worker string, in authority.ApprovalUse) (authority.ApprovalAnswer, error) {
	var out authority.ApprovalAnswer
	err := c.Do(ctx, http.MethodPost, workerPath(worker, "approvals", "use"), in, &out)
	return out, err
}

// DecideDelegated carries what a worker's orchestrator decided.
func (c *Client) DecideDelegated(ctx context.Context, worker string, in authority.WorkerDecision) (authority.Approval, error) {
	var out authority.Approval
	err := c.Do(ctx, http.MethodPost, workerPath(worker, "approvals", "decide"), in, &out)
	return out, err
}

// DelegationPolicies lists the versions of the delegation policy, newest first.
func (c *Client) DelegationPolicies(ctx context.Context) ([]authority.PolicyVersion, error) {
	var out PolicyList
	err := c.Do(ctx, http.MethodGet, "/v1/policies?kind="+string(authority.PolicyDelegation), nil, &out)
	return out.Versions, err
}

// DelegationPolicy reads one version of the delegation policy.
func (c *Client) DelegationPolicy(ctx context.Context, version int64) (authority.PolicyVersion, error) {
	var out authority.PolicyVersion
	err := c.Do(ctx, http.MethodGet, "/v1/policies/"+strconv.FormatInt(version, 10)+"?kind="+string(authority.PolicyDelegation), nil, &out)
	return out, err
}
