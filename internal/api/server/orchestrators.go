package server

import (
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

func (h *handler) orchestratorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/workers/{id}/orchestrator", h.showOrchestrator)
	mux.HandleFunc("PUT /v1/workers/{id}/orchestrator", h.member(h.setOrchestrator))
	mux.HandleFunc("POST /v1/workers/{id}/orchestrator/digest", h.workerDigest)
	mux.HandleFunc("POST /v1/workers/{id}/orchestrator/acts/{verb}", h.workerOrchestrate)
}

// workerOrchestrate is how an orchestrator's acts reach the ledger: through its worker, which proves
// itself, and which the control plane holds to the run a member asked for.
func (h *handler) workerOrchestrate(w http.ResponseWriter, r *http.Request) {
	var in ledger.OrchestratorCall
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	result, err := h.workers.Orchestrate(r.Context(), r.PathValue("verb"), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, result)
}

func (h *handler) showOrchestrator(w http.ResponseWriter, r *http.Request) {
	row, err := h.workers.Orchestrator(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, row)
}

func (h *handler) setOrchestrator(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.SetOrchestrator
	if !decode(w, r, &in) {
		return
	}
	row, err := h.workers.SetOrchestrator(r.Context(), r.PathValue("id"), in.Desired, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, row)
}

// workerDigest is the read a worker makes before it spends anything on a wake.
func (h *handler) workerDigest(w http.ResponseWriter, r *http.Request) {
	var in ledger.WakeRequest
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	digest, err := h.workers.WakeDigest(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, digest)
}
