// Package server holds the handlers of the versioned API. It composes the
// application's use cases and wires no adapter, so one handler serves every
// deployment mode.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// maxBody bounds one request. Every body is a small JSON document.
const maxBody = 1 << 20

// localMember is the actor of a call that arrives with no credential. The socket
// admits only the owning user (NFR-SEC-2), and member sign-in arrives in a later
// unit.
var localMember = ledger.Actor{Kind: ledger.ActorMember, ID: "local"}

// Config is what the handlers run on.
type Config struct {
	Scope ledger.WorkspaceID
	Store ports.Store
	// LeaseTTL is the policy's lease in milliseconds, for a claim that names none.
	LeaseTTL int64
	// Protocol is the plane protocol this control plane speaks. Zero means this
	// build's version.
	Protocol int
	// HeartbeatMs is what a worker is told to beat at.
	HeartbeatMs int64
}

type handler struct {
	Config
	workOrders *control.WorkOrders
	leases     *control.Leases
	reviews    *control.Reviews
	workers    *control.Workers
	fleet      *control.Fleet
}

// New returns the versioned API.
func New(config Config) http.Handler {
	h := &handler{
		Config:     config,
		workOrders: control.NewWorkOrders(config.Store),
		leases:     control.NewLeases(config.Store),
		reviews:    control.NewReviews(config.Store),
		fleet:      control.NewFleet(config.Store),
		workers: control.NewWorkers(config.Store, config.Scope, control.WorkerOptions{
			Protocol: config.Protocol, HeartbeatMs: config.HeartbeatMs, LeaseTTLMs: config.LeaseTTL,
		}),
	}
	mux := http.NewServeMux()
	h.workerRoutes(mux)
	mux.HandleFunc("GET /v1/health", h.health)
	mux.HandleFunc("GET /v1/sources", h.sources)
	mux.HandleFunc("POST /v1/work-orders", h.createWorkOrder)
	mux.HandleFunc("GET /v1/work-orders", h.listWorkOrders)
	mux.HandleFunc("GET /v1/work-orders/{id}", h.showWorkOrder)
	mux.HandleFunc("POST /v1/work-orders/{id}/review", h.review)
	mux.HandleFunc("POST /v1/work-orders/{id}/claims", h.claim)
	mux.HandleFunc("POST /v1/work-orders/{id}/claims/{claim}/release", h.release)
	mux.HandleFunc("POST /v1/work-orders/{id}/reassign", h.reassign)
	mux.HandleFunc("POST /v1/work-orders/{id}/lanes", h.startLane)
	mux.HandleFunc("POST /v1/work-orders/{id}/writes", h.write)
	return mux
}

func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: err.Error()})
		return false
	}
	return true
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func refuse(w http.ResponseWriter, status int, detail api.ErrorDetail) {
	reply(w, status, api.ErrorBody{Error: detail})
}

// fail turns an error from a use case into its response. Each domain error is a
// different instruction to the caller. What is left came from the store, and a
// store that cannot answer is 503, so a client retries instead of giving up.
func fail(w http.ResponseWriter, err error) {
	var fenced *workorders.FencedError
	var protocol *ledger.ProtocolError
	switch {
	case errors.As(err, &fenced):
		refuse(w, http.StatusConflict, api.ErrorDetail{
			Code: api.CodeFenced, Message: err.Error(), CurrentFence: fenced.Current, State: string(fenced.State),
		})
	case errors.As(err, &protocol):
		refuse(w, http.StatusUpgradeRequired, api.ErrorDetail{
			Code: api.CodeProtocol, Message: err.Error(), Worker: protocol.Worker, Oldest: protocol.Oldest, Newest: protocol.Newest,
		})
	case errors.Is(err, ledger.ErrBadCredential):
		refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: api.CodeBadCredential, Message: err.Error()})
	case errors.Is(err, ledger.ErrBadJoinToken):
		refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: api.CodeBadJoinToken, Message: err.Error()})
	case errors.Is(err, ledger.ErrStaleEpoch):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeStaleEpoch, Message: err.Error()})
	case errors.Is(err, ports.ErrNotFound):
		refuse(w, http.StatusNotFound, api.ErrorDetail{Code: api.CodeNotFound, Message: err.Error()})
	case errors.Is(err, workorders.ErrInvalid):
		refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: err.Error()})
	case errors.Is(err, workorders.ErrForbidden):
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeForbidden, Message: err.Error()})
	case errors.Is(err, workorders.ErrLost):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeLost, Message: err.Error()})
	case errors.Is(err, workorders.ErrIneligible):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeIneligible, Message: err.Error()})
	case errors.Is(err, workorders.ErrLapsed):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeLapsed, Message: err.Error()})
	case errors.Is(err, workorders.ErrNotLapsed):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeNotLapsed, Message: err.Error()})
	case errors.Is(err, workorders.ErrUnpushedWork):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeUnpushedWork, Message: err.Error()})
	case errors.Is(err, workorders.ErrIllegalMove), errors.Is(err, workorders.ErrLeasedEdge):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeIllegalMove, Message: err.Error()})
	default:
		refuse(w, http.StatusServiceUnavailable, api.ErrorDetail{Code: api.CodeUnavailable, Message: err.Error()})
	}
}

func invalid(w http.ResponseWriter, format string, args ...any) {
	refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: fmt.Sprintf(format, args...)})
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	var health api.Health
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		workspace, err := tx.Workspace()
		health = api.Health{Status: "ok", Revision: workspace.Rev}
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, health)
}

func (h *handler) sources(w http.ResponseWriter, r *http.Request) {
	list := api.SourceList{Sources: []planning.SourceBinding{}}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		bindings, err := tx.Planning().Bindings()
		if err == nil && bindings != nil {
			list.Sources = bindings
		}
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, list)
}

func (h *handler) createWorkOrder(w http.ResponseWriter, r *http.Request) {
	var in api.CreateWorkOrder
	if !decode(w, r, &in) {
		return
	}
	kind := in.Kind
	if kind == "" {
		kind = workorders.KindChange
	}
	record, err := h.workOrders.Record(r.Context(), h.Scope, control.LedgerWork{
		Order: workorders.WorkOrder{
			ID: in.ID, Kind: kind, Title: in.Title, RepositoryID: in.RepositoryID, Priority: in.Priority,
			Size: in.Size, Requirements: in.Requirements, Needs: in.Needs,
		},
		Statement: in.Statement,
		Criteria:  in.Criteria,
		DependsOn: in.DependsOn,
	}, localMember)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, record)
}

func (h *handler) listWorkOrders(w http.ResponseWriter, r *http.Request) {
	state := workorders.State(r.URL.Query().Get("state"))
	list := api.WorkOrderList{WorkOrders: []workorders.WorkOrder{}}
	if worker := r.URL.Query().Get("offered_to"); worker != "" {
		offered, err := h.workers.Offered(r.Context(), worker)
		if err != nil {
			fail(w, err)
			return
		}
		list.WorkOrders = offered
		reply(w, http.StatusOK, list)
		return
	}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if state == "" || row.State == state {
				list.WorkOrders = append(list.WorkOrders, row)
			}
		}
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, list)
}

func (h *handler) showWorkOrder(w http.ResponseWriter, r *http.Request) {
	var row workorders.WorkOrder
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.WorkOrders().Get(r.PathValue("id"))
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, row)
}

func (h *handler) review(w http.ResponseWriter, r *http.Request) {
	result, err := h.reviews.Pass(r.Context(), h.Scope, r.PathValue("id"), localMember)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.ReviewResult{State: result.State, Failures: result.Failures})
}

// claim serves both claimants: a worker names itself with its credential, and a
// member with no credential chooses the worker (FR-WO-9).
func (h *handler) claim(w http.ResponseWriter, r *http.Request) {
	var in api.ClaimRequest
	if !decode(w, r, &in) {
		return
	}
	if in.ClaimID == "" || in.WorkerID == "" {
		invalid(w, "a claim needs a claim_id and a worker_id")
		return
	}
	ttl := in.TTLMs
	if ttl == 0 {
		ttl = h.LeaseTTL
	}
	request := workorders.ClaimRequest{WorkOrder: r.PathValue("id"), ClaimID: in.ClaimID, Worker: in.WorkerID, TTLMs: ttl}
	var grant workorders.Grant
	var err error
	if credential := bearer(r); credential != "" {
		grant, err = h.workers.Claim(r.Context(), ledger.WorkerAuth{WorkerID: in.WorkerID, Credential: credential}, request)
	} else {
		grant, err = h.leases.Claim(r.Context(), h.Scope, request, localMember)
	}
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, grantBody(grant))
}

func grantBody(grant workorders.Grant) api.Grant {
	body := api.Grant{
		WorkOrder: grant.WorkOrder, ClaimID: grant.ClaimID, Fence: grant.Fence, TTLMs: grant.TTLMs,
		ResumeLaneID: grant.ResumeLaneID, Branch: grant.Branch, Replayed: grant.Replayed,
	}
	if grant.ContractVersion != 0 {
		body.Contract = &api.ContractPin{ID: grant.ContractID, Version: grant.ContractVersion, Digest: grant.ContractDigest}
	}
	return body
}

func (h *handler) release(w http.ResponseWriter, r *http.Request) {
	var in api.ReleaseRequest
	if !decode(w, r, &in) {
		return
	}
	lease := workorders.Lease{WorkOrder: r.PathValue("id"), ClaimID: r.PathValue("claim"), Fence: in.Fence}
	if err := h.leases.Release(r.Context(), h.Scope, lease, localMember); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) reassign(w http.ResponseWriter, r *http.Request) {
	var in api.ReassignRequest
	if !decode(w, r, &in) {
		return
	}
	if err := h.leases.Reassign(r.Context(), h.Scope, r.PathValue("id"), in.Reason, localMember); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// startLane records a member's request to run a lane (FR-WO-9). The worker holding the
// claim reads it as desired state, so this answers before anything has started.
func (h *handler) startLane(w http.ResponseWriter, r *http.Request) {
	var in api.StartLane
	if !decode(w, r, &in) {
		return
	}
	if in.LaneID == "" {
		invalid(w, "a lane needs a lane_id")
		return
	}
	lease, err := h.leases.DispatchWith(r.Context(), h.Scope, r.PathValue("id"), in.LaneID, in.Overrides, localMember)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.LaneDispatched{LaneID: in.LaneID, WorkOrder: lease.WorkOrder, ClaimID: lease.ClaimID, Fence: lease.Fence})
}

func (h *handler) write(w http.ResponseWriter, r *http.Request) {
	var in api.WriteRequest
	if !decode(w, r, &in) {
		return
	}
	if in.WriteID == "" || in.ClaimID == "" {
		invalid(w, "a write needs a write_id and a claim_id")
		return
	}
	holder := workorders.Holder{
		Lease: workorders.Lease{WorkOrder: r.PathValue("id"), ClaimID: in.ClaimID, Fence: in.Fence},
		Lane:  in.Lane, Generation: in.Generation,
	}
	event, err := h.leases.Write(r.Context(), h.Scope, holder, in.WriteID, in.Payload, localMember)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.WriteResponse{ID: event.ID, Rev: event.Rev})
}
