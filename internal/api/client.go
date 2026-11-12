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

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
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
	session    string
	askSession func(context.Context) (string, error)
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

// UseSession sends a member's session with each call.
func (c *Client) UseSession(token string) { c.session = token }

// AskSession says how to get a session when the server asks for one. A refusal changes
// nothing on the server, so the one retry cannot repeat a change, and a call that needs no
// session never asks.
func (c *Client) AskSession(ask func(context.Context) (string, error)) { c.askSession = ask }

// Do runs one call. A nil out discards the body.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	err := c.send(ctx, method, path, in, out)
	var refused *Error
	if c.askSession == nil || !errors.As(err, &refused) || refused.Detail.Code != CodeSessionRequired {
		return err
	}
	token, err := c.askSession(ctx)
	if err != nil {
		return err
	}
	c.session = token
	return c.send(ctx, method, path, in, out)
}

func (c *Client) send(ctx context.Context, method, path string, in, out any) error {
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
	if c.session != "" {
		request.Header.Set(HeaderSession, c.session)
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
	// A route that answers a file, such as a backup, writes its bytes to a writer as they come.
	if writer, ok := out.(io.Writer); ok {
		_, err := io.Copy(writer, response.Body)
		return err
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
func (c *ProjectClient) CreateWorkOrder(ctx context.Context, in CreateWorkOrder) (workorders.WorkOrder, error) {
	var out workorders.WorkOrder
	err := c.do(ctx, http.MethodPost, "/v1/work-orders", in, &out)
	return out, err
}

// WorkOrders lists work orders. An empty state lists all of them.
func (c *ProjectClient) WorkOrders(ctx context.Context, state workorders.State) ([]workorders.WorkOrder, error) {
	path := "/v1/work-orders"
	if state != "" {
		path += "?state=" + url.QueryEscape(string(state))
	}
	var out WorkOrderList
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out.WorkOrders, err
}

// Offered lists the work a worker could claim now.
func (c *ProjectClient) Offered(ctx context.Context, worker string) ([]workorders.WorkOrder, error) {
	var out WorkOrderList
	err := c.do(ctx, http.MethodGet, "/v1/work-orders?offered_to="+url.QueryEscape(worker), nil, &out)
	return out.WorkOrders, err
}

// WorkOrder reads one work order.
func (c *ProjectClient) WorkOrder(ctx context.Context, id string) (workorders.WorkOrder, error) {
	var out workorders.WorkOrder
	err := c.do(ctx, http.MethodGet, workOrderPath(id), nil, &out)
	return out, err
}

// Review records a member's passed review.
func (c *ProjectClient) Review(ctx context.Context, id string) (ReviewResult, error) {
	var out ReviewResult
	err := c.do(ctx, http.MethodPost, workOrderPath(id, "review"), struct{}{}, &out)
	return out, err
}

// Acceptance reads where a work order stands, with what stays linked to it.
func (c *ProjectClient) Acceptance(ctx context.Context, id string) (workorders.AcceptanceView, error) {
	var out workorders.AcceptanceView
	err := c.do(ctx, http.MethodGet, workOrderPath(id, "acceptance"), nil, &out)
	return out, err
}

// Accept moves completed work to `done`.
func (c *ProjectClient) Accept(ctx context.Context, id string) (Accepted, error) {
	var out Accepted
	err := c.do(ctx, http.MethodPost, workOrderPath(id, "accept"), struct{}{}, &out)
	return out, err
}

// Rework sends completed work back for another attempt.
func (c *ProjectClient) Rework(ctx context.Context, id string, in ReworkRequest) (Accepted, error) {
	var out Accepted
	err := c.do(ctx, http.MethodPost, workOrderPath(id, "rework"), in, &out)
	return out, err
}

// Notices says how each notice stands on each channel.
func (c *ProjectClient) Notices(ctx context.Context) ([]ledger.NoticeLine, error) {
	var out NoticeList
	err := c.do(ctx, http.MethodGet, "/v1/notices", nil, &out)
	return out.Notices, err
}

// CancelWork ends a work order for good.
func (c *ProjectClient) CancelWork(ctx context.Context, id string, in CancelWork) (Accepted, error) {
	var out Accepted
	err := c.do(ctx, http.MethodPost, workOrderPath(id, "cancel"), in, &out)
	return out, err
}

// StartLane asks the claim's worker to run a lane.
func (c *ProjectClient) StartLane(ctx context.Context, id string, in StartLane) (LaneDispatched, error) {
	var out LaneDispatched
	err := c.do(ctx, http.MethodPost, workOrderPath(id, "lanes"), in, &out)
	return out, err
}

// Fleet reads the fleet's mode and caps.
func (c *ProjectClient) Fleet(ctx context.Context) (Fleet, error) {
	var out Fleet
	err := c.do(ctx, http.MethodGet, "/v1/fleet", nil, &out)
	return out, err
}

// SetFleetMode changes the fleet's mode.
func (c *ProjectClient) SetFleetMode(ctx context.Context, in SetFleetMode) (Fleet, error) {
	var out Fleet
	err := c.do(ctx, http.MethodPut, "/v1/fleet/mode", in, &out)
	return out, err
}

// SetFleetCaps replaces the caps a claim is held to.
func (c *ProjectClient) SetFleetCaps(ctx context.Context, in ledger.Caps) (Fleet, error) {
	var out Fleet
	err := c.do(ctx, http.MethodPut, "/v1/fleet/caps", in, &out)
	return out, err
}

// ArchiveLane asks for a lane's worktree to be removed.
func (c *ProjectClient) ArchiveLane(ctx context.Context, workOrder string, in ArchiveLane) error {
	return c.do(ctx, http.MethodPost, workOrderPath(workOrder, "lane-archive"), in, nil)
}

// Claim requests a work order.
func (c *ProjectClient) Claim(ctx context.Context, id string, in ClaimRequest) (Grant, error) {
	var out Grant
	err := c.do(ctx, http.MethodPost, workOrderPath(id, "claims"), in, &out)
	return out, err
}

// Release gives a claim back.
func (c *ProjectClient) Release(ctx context.Context, id, claim string, in ReleaseRequest) error {
	return c.do(ctx, http.MethodPost, workOrderPath(id, "claims", claim, "release"), in, nil)
}

// Reassign ends a lapsed holder's ownership.
func (c *ProjectClient) Reassign(ctx context.Context, id string, in ReassignRequest) error {
	return c.do(ctx, http.MethodPost, workOrderPath(id, "reassign"), in, nil)
}

// Write stores one event under a fence.
func (c *ProjectClient) Write(ctx context.Context, id string, in WriteRequest) (WriteResponse, error) {
	var out WriteResponse
	err := c.do(ctx, http.MethodPost, workOrderPath(id, "writes"), in, &out)
	return out, err
}

// Sources lists the source bindings.
func (c *ProjectClient) Sources(ctx context.Context) (SourceList, error) {
	var out SourceList
	err := c.do(ctx, http.MethodGet, "/v1/sources", nil, &out)
	return out, err
}
