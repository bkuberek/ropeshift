package server

import (
	"net/http"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/ledger"
)

func (h *handler) orchestratorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/workers/{id}/orchestrator", h.showOrchestrator)
	mux.HandleFunc("PUT /v1/workers/{id}/orchestrator", h.member(h.setOrchestrator))
	mux.HandleFunc("POST /v1/workers/{id}/orchestrator/digest", h.workerDigest)
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
