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
	mux.HandleFunc("POST /v1/workers/{id}/orchestrator/schedule", h.member(h.scheduleOrchestrator))
	mux.HandleFunc("DELETE /v1/workers/{id}/orchestrator/schedule", h.member(h.cancelScheduledStart))
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
	hold, start, err := h.plane.OrchestratorFacts(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.Orchestrator{Orchestrator: row, WaitingFor: waitingFor(wait), Hold: hold, Scheduled: start})
}

// scheduleOrchestrator records a start of a worker's orchestrator for its sign-in's usage reset.
func (h *handler) scheduleOrchestrator(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	start, err := h.plane.ScheduleOrchestrator(r.Context(), r.PathValue("id"), actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, start)
}

func (h *handler) cancelScheduledStart(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	if err := h.plane.CancelScheduledStart(r.Context(), r.PathValue("id"), actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if _, err := h.plane.SetOrchestrator(r.Context(), r.PathValue("id"), in.Desired, actor); err != nil {
		fail(w, err)
		return
	}
	// The answer is what a read says after the change, so every client prints it as a read does.
	h.showOrchestrator(w, r)
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
