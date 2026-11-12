package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"ropeshift.com/ropeshift/internal/authority"
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
	// Approval is the request to approve the plan: the open one, or else the newest. Asked for one version,
	// it is that version's.
	Approval *PlanApprovalView `json:"approval,omitempty"`
	// Versions lists every version of the plan, newest first, with where its approval stands.
	Versions []PlanVersionLine `json:"versions"`
}

// PlanVersionLine is one version of a plan and where the request to approve it stands: the request's ID and
// state, or neither when nobody asked about that version.
type PlanVersionLine struct {
	Version  int64                   `json:"version"`
	Digest   string                  `json:"digest,omitempty"`
	Approval string                  `json:"approval,omitempty"`
	State    authority.ApprovalState `json:"state,omitempty"`
}

// PlanApprovalView is a request to approve a plan version with everything a member reads before
// deciding: what the version holds, what it drafts, and how it differs from the last approved one.
type PlanApprovalView struct {
	Approval authority.Approval       `json:"approval"`
	Detail   planning.PlanDetail      `json:"detail"`
	Records  []planning.Entry         `json:"records,omitempty"`
	Drafts   []planning.DraftDecision `json:"drafts,omitempty"`
}

// PlanDetailOf reads what a plan approval says of its version. A request of another kind says nothing.
func PlanDetailOf(a authority.Approval) planning.PlanDetail {
	var detail planning.PlanDetail
	if a.Kind == authority.KindPlan && a.Detail != "" {
		_ = json.Unmarshal([]byte(a.Detail), &detail)
	}
	return detail
}

// ReviewPlan starts a contract review of a plan. The client generates ID, so a retry returns the
// first answer.
type ReviewPlan struct {
	ID string `json:"id"`
}

// ReviewPlan records a work order that reads a plan and its contracts.
func (c *ProjectClient) ReviewPlan(ctx context.Context, plan string, in ReviewPlan) (workorders.WorkOrder, error) {
	var out workorders.WorkOrder
	err := c.do(ctx, http.MethodPost, "/v1/plans/"+url.PathEscape(plan)+"/reviews", in, &out)
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
func (c *ProjectClient) Resolve(ctx context.Context, id string, in Unblock) error {
	return c.do(ctx, http.MethodPost, "/v1/work-orders/"+url.PathEscape(id)+"/resolve", in, nil)
}

// Replan returns blocked work to planning.
func (c *ProjectClient) Replan(ctx context.Context, id string, in Unblock) error {
	return c.do(ctx, http.MethodPost, "/v1/work-orders/"+url.PathEscape(id)+"/replan", in, nil)
}

// Retry is why a member retries failed work. A member is held to no recovery policy, so work with no
// attempts left gets one more.
type Retry struct {
	Reason string `json:"reason"`
}

// Retried is where retried work stands, which is `ready`.
type Retried struct {
	WorkOrder string           `json:"work_order"`
	State     workorders.State `json:"state"`
}

// Retry returns failed work to ready.
func (c *ProjectClient) Retry(ctx context.Context, id string, in Retry) (Retried, error) {
	var out Retried
	err := c.do(ctx, http.MethodPost, "/v1/work-orders/"+url.PathEscape(id)+"/retry", in, &out)
	return out, err
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
	// Record is the record the ledger keeps of this file, `<kind>:<id>`: a planning document a plan
	// recorded, or a goal, a plan, or a contract. The documents view names it, so a page lists the file
	// once, where that record shows, and not among the repository's other files.
	Record string `json:"record,omitempty"`
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

// Where a version of a planning document stands in review: covered by a plan version a member approved,
// listed by a plan version whose approval waits, or neither (FR-DOC-3).
const (
	ReviewApproved = "approved"
	ReviewPending  = "pending"
	ReviewNone     = "none"
)

// RecordedVersion is one version of a planning document the ledger keeps: its number, its digest, the
// file and commit it came from, when intake recorded it, and where it stands in review. PlanVersion is
// the plan version that covers it, or that waits to.
type RecordedVersion struct {
	Version     int64  `json:"version"`
	Digest      string `json:"digest"`
	Source      string `json:"source,omitempty"`
	RecordedAt  int64  `json:"recorded_at"`
	Review      string `json:"review"`
	PlanVersion int64  `json:"plan_version,omitempty"`
}

// RecordedDocument is a planning document intake recorded from a plan's files, with every version,
// newest first. ID is its reference, `<kind>:<id>`, and Path the file its newest version came from.
type RecordedDocument struct {
	ID       string            `json:"id"`
	Kind     planning.FileKind `json:"kind"`
	Title    string            `json:"title"`
	Path     string            `json:"path,omitempty"`
	Versions []RecordedVersion `json:"versions"`
}

// TracedWork is a work order that carries a criterion, and where it stands.
type TracedWork struct {
	ID    string           `json:"id"`
	Title string           `json:"title"`
	State workorders.State `json:"state"`
}

// TracedCriterion is a criterion of a contract's newest version that serves a requirement, with the work
// orders of that contract.
type TracedCriterion struct {
	Contract        string       `json:"contract"`
	ContractTitle   string       `json:"contract_title"`
	ContractVersion int64        `json:"contract_version"`
	Criterion       string       `json:"criterion"`
	Text            string       `json:"text"`
	WorkOrders      []TracedWork `json:"work_orders"`
}

// RequirementTrace is one requirement a plan's requirements document holds, and the criteria that serve
// it (FR-DOC-7). Document is the requirements document's reference.
type RequirementTrace struct {
	Key      string            `json:"key"`
	Text     string            `json:"text"`
	Document string            `json:"document"`
	Criteria []TracedCriterion `json:"criteria"`
	// Status is met for a requirement the code already meets, and Evidence the files and tests that show it
	// as `<repository>:<path>` (ADR-0023). It is met by existing code, and needs no criterion or verdict.
	Status   string   `json:"status,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

// PlanDocuments is what a plan's files recorded: each document with its versions, and how each
// requirement traces to criteria and work.
type PlanDocuments struct {
	Plan         planning.Plan      `json:"plan"`
	Documents    []RecordedDocument `json:"documents"`
	Requirements []RequirementTrace `json:"requirements"`
}

// RecordedRead is one version of a planning document with the body the ledger stored, beside every
// version of it, so a page reads what a contract cited after the file has moved on. AsOf is database
// time at the read, which a page counts each version's age from.
type RecordedRead struct {
	AsOf     int64            `json:"as_of"`
	Document RecordedDocument `json:"document"`
	Plan     *planning.Plan   `json:"plan,omitempty"`
	Version  RecordedVersion  `json:"version"`
	Body     string           `json:"body"`
}

// RecordedDocument reads one version of a planning document, by its reference, with its stored body. A
// version of 0 reads the newest.
func (c *ProjectClient) RecordedDocument(ctx context.Context, id string, version int64) (RecordedRead, error) {
	values := url.Values{"id": {id}}
	if version != 0 {
		values.Set("version", strconv.FormatInt(version, 10))
	}
	var out RecordedRead
	err := c.do(ctx, http.MethodGet, "/v1/documents/recorded?"+values.Encode(), nil, &out)
	return out, err
}

// Plans lists plans.
func (c *ProjectClient) Plans(ctx context.Context) ([]planning.Plan, error) {
	var out PlanList
	err := c.do(ctx, http.MethodGet, "/v1/plans", nil, &out)
	return out.Plans, err
}

// Plan reads one plan with what hangs off it.
func (c *ProjectClient) Plan(ctx context.Context, id string) (PlanView, error) {
	var out PlanView
	err := c.do(ctx, http.MethodGet, "/v1/plans/"+url.PathEscape(id), nil, &out)
	return out, err
}

// PlanVersion reads one plan with the request to approve one of its versions, which is a refusal for a
// version the plan never had.
func (c *ProjectClient) PlanVersion(ctx context.Context, id string, version int64) (PlanView, error) {
	var out PlanView
	err := c.do(ctx, http.MethodGet, "/v1/plans/"+url.PathEscape(id)+"?version="+strconv.FormatInt(version, 10), nil, &out)
	return out, err
}

// Requirements lists every requirement's newest version.
func (c *ProjectClient) Requirements(ctx context.Context) ([]planning.Requirement, error) {
	var out RequirementList
	err := c.do(ctx, http.MethodGet, "/v1/requirements", nil, &out)
	return out.Requirements, err
}

// Requirement reads a requirement's newest version by its key.
func (c *ProjectClient) Requirement(ctx context.Context, key string) (planning.Requirement, error) {
	var out planning.Requirement
	err := c.do(ctx, http.MethodGet, "/v1/requirements/"+url.PathEscape(key), nil, &out)
	return out, err
}

// SendBack returns a work order in review to planning.
func (c *ProjectClient) SendBack(ctx context.Context, id string, in SendBack) error {
	return c.do(ctx, http.MethodPost, workOrderPath(id, "send-back"), in, nil)
}

// Documents lists every source's documents.
func (c *ProjectClient) Documents(ctx context.Context) ([]Document, error) {
	var out DocumentList
	err := c.do(ctx, http.MethodGet, "/v1/documents", nil, &out)
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
func (c *ProjectClient) Document(ctx context.Context, ref DocumentRef) (Document, error) {
	var out Document
	err := c.do(ctx, http.MethodGet, "/v1/documents/read?"+ref.query(), nil, &out)
	return out, err
}

// DocumentVersions lists one document's versions, newest first.
func (c *ProjectClient) DocumentVersions(ctx context.Context, ref DocumentRef) ([]DocumentVersion, error) {
	var out DocumentVersions
	err := c.do(ctx, http.MethodGet, "/v1/documents/versions?"+ref.query(), nil, &out)
	return out.Versions, err
}
