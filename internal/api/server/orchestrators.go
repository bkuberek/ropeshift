package server

import (
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
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
	result, err := h.plane.Orchestrate(r.Context(), r.PathValue("verb"), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, result)
}

func (h *handler) showOrchestrator(w http.ResponseWriter, r *http.Request) {
	row, wait, err := h.plane.OrchestratorStanding(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.Orchestrator{Orchestrator: row, WaitingFor: waitingFor(wait)})
}

// waitingFor names what holds an orchestrator's run: a run window, work, or nothing.
func waitingFor(wait control.OrchestratorWait) string {
	switch wait {
	case control.WaitsForRunWindow:
		return api.WaitingForRunWindow
	case control.WaitsForWork:
		return api.WaitingForWork
	}
	return ""
}

func (h *handler) setOrchestrator(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.SetOrchestrator
	if !decode(w, r, &in) {
		return
	}
	row, err := h.plane.SetOrchestrator(r.Context(), r.PathValue("id"), in.Desired, actor)
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
	digest, err := h.plane.WakeDigest(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, digest)
}
