package api

import (
	"context"
	"net/http"
	"net/url"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// PlanList is the answer to a list of plans.
type PlanList struct {
	Plans []planning.Plan `json:"plans"`
}

// PlanView is one plan with what hangs off it: its goal, each contract's newest version,
// and the work orders of those contracts.
type PlanView struct {
	Plan       planning.Plan          `json:"plan"`
	Goal       *planning.Goal         `json:"goal,omitempty"`
	Contracts  []planning.Contract    `json:"contracts"`
	WorkOrders []workorders.WorkOrder `json:"work_orders"`
}

// ReviewPlan starts a contract review of a plan. The client generates ID, so a retry returns the
// first answer.
type ReviewPlan struct {
	ID string `json:"id"`
}

// ReviewPlan records a work order that reads a plan and its contracts.
func (c *Client) ReviewPlan(ctx context.Context, plan string, in ReviewPlan) (workorders.WorkOrder, error) {
	var out workorders.WorkOrder
	err := c.Do(ctx, http.MethodPost, "/v1/plans/"+url.PathEscape(plan)+"/reviews", in, &out)
	return out, err
}

// RequirementList is the answer to a list of requirements.
type RequirementList struct {
	Requirements []planning.Requirement `json:"requirements"`
}

// SendBack returns work to planning, because its contract must change.
type SendBack struct {
	Reason string `json:"reason"`
}

// Unblock takes work out of `blocked`, and Reason says what changed.
type Unblock struct {
	Reason string `json:"reason"`
}

// Resolve returns blocked work to ready.
func (c *Client) Resolve(ctx context.Context, id string, in Unblock) error {
	return c.Do(ctx, http.MethodPost, "/v1/work-orders/"+url.PathEscape(id)+"/resolve", in, nil)
}

// Replan returns blocked work to planning.
func (c *Client) Replan(ctx context.Context, id string, in Unblock) error {
	return c.Do(ctx, http.MethodPost, "/v1/work-orders/"+url.PathEscape(id)+"/replan", in, nil)
}

// Document is one document at one version. A list leaves Body empty.
type Document struct {
	Adapter  string `json:"adapter"`
	Instance string `json:"instance"`
	Path     string `json:"path"`
	Title    string `json:"title"`
	// Kind says which of requirements, specs, architecture documents, experience designs, milestones, and designs this is, or none of them.
	Kind    planning.DocumentKind `json:"kind"`
	Version string                `json:"version"`
	Digest  string                `json:"digest"`
	Source  string                `json:"source"`
	Body    string                `json:"body,omitempty"`
}

// DocumentList is the answer to a list of documents.
type DocumentList struct {
	Documents []Document `json:"documents"`
}

// DocumentVersion is one version of one document.
type DocumentVersion struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// DocumentVersions is the answer to a list of one document's versions.
type DocumentVersions struct {
	Versions []DocumentVersion `json:"versions"`
}

// Plans lists plans.
func (c *Client) Plans(ctx context.Context) ([]planning.Plan, error) {
	var out PlanList
	err := c.Do(ctx, http.MethodGet, "/v1/plans", nil, &out)
	return out.Plans, err
}

// Plan reads one plan with what hangs off it.
func (c *Client) Plan(ctx context.Context, id string) (PlanView, error) {
	var out PlanView
	err := c.Do(ctx, http.MethodGet, "/v1/plans/"+url.PathEscape(id), nil, &out)
	return out, err
}

// Requirements lists every requirement's newest version.
func (c *Client) Requirements(ctx context.Context) ([]planning.Requirement, error) {
	var out RequirementList
	err := c.Do(ctx, http.MethodGet, "/v1/requirements", nil, &out)
	return out.Requirements, err
}

// Requirement reads a requirement's newest version by its key.
func (c *Client) Requirement(ctx context.Context, key string) (planning.Requirement, error) {
	var out planning.Requirement
	err := c.Do(ctx, http.MethodGet, "/v1/requirements/"+url.PathEscape(key), nil, &out)
	return out, err
}

// SendBack returns a work order in review to planning.
func (c *Client) SendBack(ctx context.Context, id string, in SendBack) error {
	return c.Do(ctx, http.MethodPost, workOrderPath(id, "send-back"), in, nil)
}

// Documents lists every source's documents.
func (c *Client) Documents(ctx context.Context) ([]Document, error) {
	var out DocumentList
	err := c.Do(ctx, http.MethodGet, "/v1/documents", nil, &out)
	return out.Documents, err
}

// DocumentRef names one document. An empty instance takes the first source that holds the
// path, and an empty version takes the current one.
type DocumentRef struct {
	Path     string
	Instance string
	Version  string
}

func (r DocumentRef) query() string {
	values := url.Values{"path": {r.Path}}
	if r.Instance != "" {
		values.Set("instance", r.Instance)
	}
	if r.Version != "" {
		values.Set("version", r.Version)
	}
	return values.Encode()
}

// Document reads one document.
func (c *Client) Document(ctx context.Context, ref DocumentRef) (Document, error) {
	var out Document
	err := c.Do(ctx, http.MethodGet, "/v1/documents/read?"+ref.query(), nil, &out)
	return out, err
}

// DocumentVersions lists one document's versions, newest first.
func (c *Client) DocumentVersions(ctx context.Context, ref DocumentRef) ([]DocumentVersion, error) {
	var out DocumentVersions
	err := c.Do(ctx, http.MethodGet, "/v1/documents/versions?"+ref.query(), nil, &out)
	return out.Versions, err
}
