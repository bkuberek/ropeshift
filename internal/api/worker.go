package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/questions"
)

// More error codes, for the worker's side of the API.
const (
	CodeBadCredential = "bad_credential"
	CodeBadJoinToken  = "bad_join_token"
	CodeStaleEpoch    = "stale_epoch"
	CodeProtocol      = "protocol"
)

// JoinTokenResponse carries a new join token. It is shown once, and the ledger keeps
// only its digest.
type JoinTokenResponse struct {
	Token string `json:"token"`
}

// WorkerList is the answer to a list of workers.
type WorkerList struct {
	Workers []ledger.Worker `json:"workers"`
}

// WithCredential returns a client that identifies a worker on every call.
func (c *Client) WithCredential(credential string) *Client {
	copied := *c
	copied.credential = credential
	return &copied
}

func workerPath(id string, rest ...string) string {
	path := "/v1/workers/" + url.PathEscape(id)
	for _, part := range rest {
		path += "/" + part
	}
	return path
}

// CreateJoinToken makes a join token.
func (c *Client) CreateJoinToken(ctx context.Context) (string, error) {
	var out JoinTokenResponse
	err := c.Do(ctx, http.MethodPost, "/v1/workers/tokens", struct{}{}, &out)
	return out.Token, err
}

// Workers lists the registered workers.
func (c *Client) Workers(ctx context.Context) ([]ledger.Worker, error) {
	var out WorkerList
	err := c.Do(ctx, http.MethodGet, "/v1/workers", nil, &out)
	return out.Workers, err
}

// Register exchanges a join token for a worker credential.
func (c *Client) Register(ctx context.Context, in ledger.Registration) (ledger.WorkerCredential, error) {
	var out ledger.WorkerCredential
	err := c.Do(ctx, http.MethodPost, "/v1/workers/register", in, &out)
	return out, err
}

// OpenSession opens a new worker epoch.
func (c *Client) OpenSession(ctx context.Context, worker string, in ledger.SessionRequest) (ledger.Session, error) {
	var out ledger.Session
	err := c.Do(ctx, http.MethodPost, workerPath(worker, "sessions"), in, &out)
	return out, err
}

// Heartbeat sends one heartbeat.
func (c *Client) Heartbeat(ctx context.Context, worker string, in ledger.Heartbeat) (ledger.HeartbeatReply, error) {
	var out ledger.HeartbeatReply
	err := c.Do(ctx, http.MethodPost, workerPath(worker, "heartbeat"), in, &out)
	return out, err
}

// Desired reads desired state.
func (c *Client) Desired(ctx context.Context, worker string, since int64) (ledger.DesiredState, error) {
	var out ledger.DesiredState
	path := workerPath(worker, "desired") + "?since=" + strconv.FormatInt(since, 10)
	err := c.Do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// Ask records a question for a lane this worker holds.
func (c *Client) Ask(ctx context.Context, worker string, in questions.LaneAsk) (questions.AskResult, error) {
	var out questions.AskResult
	err := c.Do(ctx, http.MethodPost, workerPath(worker, "ask"), in, &out)
	return out, err
}

// Upload sends one batch of events.
func (c *Client) Upload(ctx context.Context, worker string, in ledger.UploadBatch) (ledger.UploadReply, error) {
	var out ledger.UploadReply
	err := c.Do(ctx, http.MethodPost, workerPath(worker, "events"), in, &out)
	return out, err
}

// LaneList is the answer to a list of lanes.
type LaneList struct {
	Lanes []ledger.LaneView `json:"lanes"`
}

// Lanes reads every lane, each with its freshness.
func (c *Client) Lanes(ctx context.Context) ([]ledger.LaneView, error) {
	var out LaneList
	err := c.Do(ctx, http.MethodGet, "/v1/lanes", nil, &out)
	return out.Lanes, err
}

// Lane reads one lane, with its freshness.
func (c *Client) Lane(ctx context.Context, id string) (ledger.LaneView, error) {
	var out ledger.LaneView
	err := c.Do(ctx, http.MethodGet, "/v1/lanes/"+url.PathEscape(id), nil, &out)
	return out, err
}

// LaneReset says which work order's lane a member reset.
type LaneReset struct {
	WorkOrder string `json:"work_order"`
}

// ResetLane resets the breaker of a work order's lane.
func (c *Client) ResetLane(ctx context.Context, workOrder string) (LaneReset, error) {
	var out LaneReset
	err := c.Do(ctx, http.MethodPost, "/v1/work-orders/"+url.PathEscape(workOrder)+"/lane-reset", nil, &out)
	return out, err
}

// WatchEvent is the data of one server-sent event: a hint, which carries no state.
type WatchEvent struct {
	Rev int64 `json:"rev"`
}

// Watch streams revision hints. The stream is an ordinary outbound request, and
// Last-Event-ID resumes it. The channel closes when the stream ends.
func (c *Client) Watch(ctx context.Context, worker string, after int64) (<-chan int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+workerPath(worker, "watch"), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", strconv.FormatInt(after, 10))
	if c.credential != "" {
		request.Header.Set("Authorization", "Bearer "+c.credential)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		defer response.Body.Close()
		return nil, readError(response)
	}
	hints := make(chan int64, 1)
	go func() {
		defer close(hints)
		defer response.Body.Close()
		lines := bufio.NewScanner(response.Body)
		for lines.Scan() {
			data, ok := strings.CutPrefix(lines.Text(), "data: ")
			if !ok {
				continue
			}
			var event WatchEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				return
			}
			select {
			case hints <- event.Rev:
			default:
			}
		}
	}()
	return hints, nil
}
