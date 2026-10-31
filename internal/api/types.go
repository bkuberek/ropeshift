package api

import (
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// The error codes of the API. A client decides on the code, never on the text.
const (
	CodeInvalid     = "invalid"
	CodeNotFound    = "not_found"
	CodeForbidden   = "forbidden"
	CodeLost        = "lost"
	CodeIneligible  = "ineligible"
	CodeFenced      = "fenced"
	CodeLapsed      = "lapsed"
	CodeNotLapsed   = "not_lapsed"
	CodeIllegalMove = "illegal_move"
	CodeUnavailable = "unavailable"
)

// ErrorBody is every error response.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail says what went wrong. A fenced write carries the current fence
// (FR-WO-3), so a holder learns what replaced it.
type ErrorDetail struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	CurrentFence int64  `json:"current_fence,omitempty"`
	State        string `json:"state,omitempty"`
	// Oldest and Newest say which protocols a refusing control plane accepts.
	Worker int `json:"worker_protocol,omitempty"`
	Oldest int `json:"oldest_protocol,omitempty"`
	Newest int `json:"newest_protocol,omitempty"`
}

// Health answers only after the server read the store.
type Health struct {
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}

// CreateWorkOrder asks for a work order that lives only in the ledger, with the
// contract it pins. The client generates ID, so a retry returns the first answer.
type CreateWorkOrder struct {
	ID           string               `json:"id"`
	Kind         workorders.Kind      `json:"kind,omitempty"`
	Title        string               `json:"title"`
	Statement    string               `json:"statement,omitempty"`
	Criteria     []planning.Criterion `json:"criteria,omitempty"`
	Priority     int64                `json:"priority,omitempty"`
	Size         string               `json:"size,omitempty"`
	Requirements []string             `json:"requirements,omitempty"`
	Needs        map[string]string    `json:"needs,omitempty"`
	DependsOn    []string             `json:"depends_on,omitempty"`
	RepositoryID string               `json:"repository_id,omitempty"`
}

// WorkOrderList is the answer to a list.
type WorkOrderList struct {
	WorkOrders []workorders.WorkOrder `json:"work_orders"`
}

// ReviewResult says where a passed review left a work order.
type ReviewResult struct {
	State    workorders.State          `json:"state"`
	Failures []workorders.ReadyFailure `json:"failures,omitempty"`
}

// ClaimRequest asks for a work order. The client generates ClaimID, so a retry
// after a timeout returns the original grant. A TTLMs of zero takes the policy's
// lease.
type ClaimRequest struct {
	ClaimID  string `json:"claim_id"`
	WorkerID string `json:"worker_id"`
	TTLMs    int64  `json:"ttl_ms,omitempty"`
}

// ContractPin names the contract version a work order pinned.
type ContractPin struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Digest  string `json:"digest"`
}

// Grant is everything a lane needs to start.
type Grant struct {
	WorkOrder    string       `json:"work_order"`
	ClaimID      string       `json:"claim_id"`
	Fence        int64        `json:"fence"`
	TTLMs        int64        `json:"ttl_ms"`
	ResumeLaneID string       `json:"resume_lane_id,omitempty"`
	Branch       string       `json:"branch,omitempty"`
	Contract     *ContractPin `json:"contract,omitempty"`
	Replayed     bool         `json:"replayed,omitempty"`
}

// StartLane asks the worker holding a claim to run a lane. The client generates
// LaneID, so a retry names the same lane.
type StartLane struct {
	LaneID string `json:"lane_id"`
}

// LaneDispatched says which lease a lane will run under.
type LaneDispatched struct {
	LaneID    string `json:"lane_id"`
	WorkOrder string `json:"work_order"`
	ClaimID   string `json:"claim_id"`
	Fence     int64  `json:"fence"`
}

// ReleaseRequest gives a claim back.
type ReleaseRequest struct {
	Fence int64 `json:"fence"`
}

// ReassignRequest ends a lapsed holder's ownership.
type ReassignRequest struct {
	Reason string `json:"reason"`
}

// WriteRequest is one write under a fence. The client generates WriteID, so a write
// retried after a lost answer stores one event.
type WriteRequest struct {
	WriteID    string `json:"write_id"`
	ClaimID    string `json:"claim_id"`
	Fence      int64  `json:"fence"`
	Lane       string `json:"lane,omitempty"`
	Generation int64  `json:"generation,omitempty"`
	Payload    []byte `json:"payload,omitempty"`
}

// WriteResponse names the event a write stored.
type WriteResponse struct {
	ID  string `json:"id"`
	Rev int64  `json:"rev"`
}

// SourceList is the answer to a list of sources.
type SourceList struct {
	Sources []planning.SourceBinding `json:"sources"`
}
