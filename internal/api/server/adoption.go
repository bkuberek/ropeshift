package server

import (
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
)

// adoptionRoutes plan an adoption into a project. Planning reads paths on the machine of the worker this daemon
// runs, so it stays behind a member's session.
func (h *handler) adoptionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/adoption/plan", h.member(h.planAdoption))
}

// newAdoptionRoutes plan an adoption into a project the adoption has yet to create, which has no route of its
// own: the plan reads no record of any project.
func (h *handler) newAdoptionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/adoption/plan", h.member(h.planNewAdoption))
}

// planNewAdoption plans an adoption into a project that does not exist yet.
func (h *handler) planNewAdoption(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	h.plan(w, r, actor, true)
}

// planAdoption plans an adoption into the project the route names.
func (h *handler) planAdoption(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	h.plan(w, r, actor, false)
}

// plan reads the layout around a clone on the worker's machine and answers the plan of adopting it, which
// records nothing (FR-ADOPT-12).
func (h *handler) plan(w http.ResponseWriter, r *http.Request, actor ledger.Actor, newProject bool) {
	var in api.AdoptionPlanRequest
	if !decode(w, r, &in) {
		return
	}
	in.NewProject = in.NewProject || newProject
	if h.Checkouts == nil || h.Layouts == nil {
		refuse(w, http.StatusNotImplemented, api.ErrorDetail{Code: api.CodeUnavailable, Message: "this daemon runs no worker whose clones it can read"})
		return
	}
	plan, err := h.adoptions.Plan(r.Context(), h.Scope, control.AdoptionRequest{
		Root: in.Root, Paths: in.Paths, Depth: in.Depth, Planning: in.Planning, Design: in.Design, LeaveOut: in.LeaveOut, NewProject: in.NewProject,
	}, h.Checkouts, h.Layouts, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, plan)
}
