package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// ErrUnreachable means nothing answered, so the caller learned nothing about the
// ledger.
var ErrUnreachable = errors.New("control plane unreachable")

// Error is a refusal the server explained.
type Error struct {
	Status int
	Detail ErrorDetail
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Detail.Code, e.Detail.Message)
}

// Client is the one client of the API. Every surface uses it, so no two can drift.
type Client struct {
	http       *http.Client
	base       string
	credential string
}

// NewUnixClient returns a client on a unix socket. A daemon on the local machine
// needs no host name, so the URL's host is only a placeholder.
func NewUnixClient(socket string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		},
	}
	return &Client{http: &http.Client{Transport: transport}, base: "http://ropeshift"}
}

// Close releases idle connections.
func (c *Client) Close() { c.http.CloseIdleConnections() }

// Do runs one call. A nil out discards the body.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.credential != "" {
		request.Header.Set("Authorization", "Bearer "+c.credential)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return readError(response)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}

func readError(response *http.Response) error {
	var body ErrorBody
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return &Error{Status: response.StatusCode, Detail: ErrorDetail{Code: CodeUnavailable, Message: response.Status}}
	}
	return &Error{Status: response.StatusCode, Detail: body.Error}
}

func workOrderPath(id string, rest ...string) string {
	path := "/v1/work-orders/" + url.PathEscape(id)
	for _, part := range rest {
		path += "/" + url.PathEscape(part)
	}
	return path
}

// Health reads the server's health.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var out Health
	err := c.Do(ctx, http.MethodGet, "/v1/health", nil, &out)
	return out, err
}

// CreateWorkOrder records a work order and its contract in the ledger.
func (c *Client) CreateWorkOrder(ctx context.Context, in CreateWorkOrder) (workorders.WorkOrder, error) {
	var out workorders.WorkOrder
	err := c.Do(ctx, http.MethodPost, "/v1/work-orders", in, &out)
	return out, err
}

// WorkOrders lists work orders. An empty state lists all of them.
func (c *Client) WorkOrders(ctx context.Context, state workorders.State) ([]workorders.WorkOrder, error) {
	path := "/v1/work-orders"
	if state != "" {
		path += "?state=" + url.QueryEscape(string(state))
	}
	var out WorkOrderList
	err := c.Do(ctx, http.MethodGet, path, nil, &out)
	return out.WorkOrders, err
}

// WorkOrder reads one work order.
func (c *Client) WorkOrder(ctx context.Context, id string) (workorders.WorkOrder, error) {
	var out workorders.WorkOrder
	err := c.Do(ctx, http.MethodGet, workOrderPath(id), nil, &out)
	return out, err
}

// Review records a member's passed review.
func (c *Client) Review(ctx context.Context, id string) (ReviewResult, error) {
	var out ReviewResult
	err := c.Do(ctx, http.MethodPost, workOrderPath(id, "review"), struct{}{}, &out)
	return out, err
}

// StartLane asks the claim's worker to run a lane.
func (c *Client) StartLane(ctx context.Context, id string, in StartLane) (LaneDispatched, error) {
	var out LaneDispatched
	err := c.Do(ctx, http.MethodPost, workOrderPath(id, "lanes"), in, &out)
	return out, err
}

// Fleet reads the fleet's mode and caps.
func (c *Client) Fleet(ctx context.Context) (Fleet, error) {
	var out Fleet
	err := c.Do(ctx, http.MethodGet, "/v1/fleet", nil, &out)
	return out, err
}

// SetFleetMode changes the fleet's mode.
func (c *Client) SetFleetMode(ctx context.Context, in SetFleetMode) (Fleet, error) {
	var out Fleet
	err := c.Do(ctx, http.MethodPut, "/v1/fleet/mode", in, &out)
	return out, err
}

// SetFleetCaps replaces the caps a claim is held to.
func (c *Client) SetFleetCaps(ctx context.Context, in ledger.Caps) (Fleet, error) {
	var out Fleet
	err := c.Do(ctx, http.MethodPut, "/v1/fleet/caps", in, &out)
	return out, err
}

// ArchiveLane asks for a lane's worktree to be removed.
func (c *Client) ArchiveLane(ctx context.Context, workOrder string, in ArchiveLane) error {
	return c.Do(ctx, http.MethodPost, workOrderPath(workOrder, "lane-archive"), in, nil)
}

// Claim requests a work order.
func (c *Client) Claim(ctx context.Context, id string, in ClaimRequest) (Grant, error) {
	var out Grant
	err := c.Do(ctx, http.MethodPost, workOrderPath(id, "claims"), in, &out)
	return out, err
}

// Release gives a claim back.
func (c *Client) Release(ctx context.Context, id, claim string, in ReleaseRequest) error {
	return c.Do(ctx, http.MethodPost, workOrderPath(id, "claims", claim, "release"), in, nil)
}

// Reassign ends a lapsed holder's ownership.
func (c *Client) Reassign(ctx context.Context, id string, in ReassignRequest) error {
	return c.Do(ctx, http.MethodPost, workOrderPath(id, "reassign"), in, nil)
}

// Write stores one event under a fence.
func (c *Client) Write(ctx context.Context, id string, in WriteRequest) (WriteResponse, error) {
	var out WriteResponse
	err := c.Do(ctx, http.MethodPost, workOrderPath(id, "writes"), in, &out)
	return out, err
}

// Sources lists the source bindings.
func (c *Client) Sources(ctx context.Context) (SourceList, error) {
	var out SourceList
	err := c.Do(ctx, http.MethodGet, "/v1/sources", nil, &out)
	return out, err
}
