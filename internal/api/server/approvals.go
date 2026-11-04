package server

import (
	"net/http"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/ledger"
)

func (h *handler) approvalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/approvals", h.listApprovals)
	mux.HandleFunc("GET /v1/approvals/{id}", h.showApproval)
	mux.HandleFunc("POST /v1/approvals/{id}/decide", h.member(h.decideApproval))
	mux.HandleFunc("POST /v1/workers/{id}/approvals/use", h.workerUseApproval)
	mux.HandleFunc("POST /v1/workers/{id}/approvals/decide", h.workerDecideApproval)
}

func (h *handler) listApprovals(w http.ResponseWriter, r *http.Request) {
	rows, err := h.approvals.List(r.Context(), h.Scope, authority.ApprovalState(r.URL.Query().Get("state")))
	if err != nil {
		fail(w, err)
		return
	}
	if rows == nil {
		rows = []authority.Approval{}
	}
	reply(w, http.StatusOK, api.ApprovalList{Approvals: rows})
}

func (h *handler) showApproval(w http.ResponseWriter, r *http.Request) {
	row, err := h.approvals.Get(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, row)
}

// decideApproval is a member's word. The session this route demands is the one thing a lane cannot
// present, and a decision that names a digest other than the request's is refused.
func (h *handler) decideApproval(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.DecideApproval
	if !decode(w, r, &in) {
		return
	}
	if in.Digest == "" {
		invalid(w, "a decision names the digest it saw")
		return
	}
	record, err := h.approvals.Decide(r.Context(), h.Scope, authority.Decision{
		ID: r.PathValue("id"), Digest: in.Digest, Allow: in.Allow, Reason: in.Reason,
	}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, record)
}

// workerUseApproval is what a lane's hook polls, through its worker.
func (h *handler) workerUseApproval(w http.ResponseWriter, r *http.Request) {
	var in authority.ApprovalUse
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	answer, err := h.workers.UseApproval(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, answer)
}

// workerDecideApproval is how an orchestrator's decision reaches the ledger: through its worker,
// which proves itself, and which the control plane holds to the policy in force.
func (h *handler) workerDecideApproval(w http.ResponseWriter, r *http.Request) {
	var in authority.WorkerDecision
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	record, err := h.workers.DecideApproval(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, record)
}
