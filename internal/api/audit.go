package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// AuditPath is a log's audit: the installation's at the top, and a project's under its routes.
const AuditPath = "/v1/audit"

// The audit's bounds. A page holds at most AuditMaxLimit events, and a filtered read looks at most
// AuditScanLimit events before it answers with what it found, so no filter makes a long read.
const (
	AuditDefaultLimit = 50
	AuditMaxLimit     = 200
	AuditScanLimit    = 5000
)

// AuditQuery narrows an audit. Every field is optional: Actor is an actor kind, Kind the start of an
// event type ("project." matches every project event), Subject a subject kind, and Before the revision
// a page starts below, zero for the newest.
type AuditQuery struct {
	Actor   string
	ActorID string
	Kind    string
	Subject string
	Before  int64
	Limit   int
}

// Encode is the query a route reads.
func (q AuditQuery) Encode() string {
	values := url.Values{}
	for name, value := range map[string]string{"actor": q.Actor, "actor_id": q.ActorID, "kind": q.Kind, "subject": q.Subject} {
		if value != "" {
			values.Set(name, value)
		}
	}
	if q.Before > 0 {
		values.Set("before", strconv.FormatInt(q.Before, 10))
	}
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

// AuditEvent is one event as the audit shows it: who did what to which record, when, and the event's
// whole record. Data is the event's own record, which an agent's words can be part of, so a surface
// shows it as text.
type AuditEvent struct {
	StreamEvent
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data,omitempty"`
}

// AuditPage is one page of an audit, newest first. Head is the log's newest revision, and Next the
// revision the next page starts below, zero when no older event is left. Scanned says the read stopped
// at its bound with older events left unread.
type AuditPage struct {
	Scope   string       `json:"scope"`
	AsOf    int64        `json:"as_of"`
	Head    int64        `json:"head"`
	Next    int64        `json:"next"`
	Scanned bool         `json:"scanned,omitempty"`
	Events  []AuditEvent `json:"events"`
}

// InstallationAudit reads the installation's own log: projects, members, workers, and the roster's
// installation layer.
func (c *Client) InstallationAudit(ctx context.Context, q AuditQuery) (AuditPage, error) {
	var out AuditPage
	err := c.Do(ctx, http.MethodGet, AuditPath+q.Encode(), nil, &out)
	return out, err
}

// Audit reads one project's log.
func (p *ProjectClient) Audit(ctx context.Context, q AuditQuery) (AuditPage, error) {
	var out AuditPage
	err := p.do(ctx, http.MethodGet, AuditPath+q.Encode(), nil, &out)
	return out, err
}
