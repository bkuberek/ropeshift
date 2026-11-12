package server

import (
	"errors"
	"net/http"
	"strconv"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/roster"
)

// installationRosterRoutes serve the installation's layer of the agent roster, and projectRosterRoutes a
// project's (ADR-0025). Every change runs behind a member's session, because changing the roster is a
// consent operation and no delegate's word runs one (FR-AGENT-7). The routes the roster editor needs
// read and change both scopes the same way.
func (h *handler) installationRosterRoutes(mux *http.ServeMux) {
	h.rosterRoutes(mux, roster.ScopeInstallation)
}

func (h *handler) projectRosterRoutes(mux *http.ServeMux) {
	h.rosterRoutes(mux, roster.ScopeProject)
}

func (h *handler) rosterRoutes(mux *http.ServeMux, scope string) {
	mux.HandleFunc("GET /v1/roster", func(w http.ResponseWriter, r *http.Request) { h.showRoster(w, r, scope) })
	mux.HandleFunc("GET /v1/roster/history", func(w http.ResponseWriter, r *http.Request) { h.rosterHistory(w, r, scope) })
	mux.HandleFunc("POST /v1/roster/changes", h.member(func(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
		h.changeRoster(w, r, scope, actor)
	}))
	mux.HandleFunc("POST /v1/roster/revert", h.member(func(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
		h.revertRoster(w, r, scope, actor)
	}))
}

func (h *handler) rosterOps() *control.Roster { return control.NewRoster(h.Installation, h.Projects) }

func (h *handler) rosterTarget(scope string) control.RosterTarget {
	return control.RosterTarget{Scope: scope, Store: h.Store, Project: h.Scope}
}

// sameScope refuses a body whose scope is not the route's, so a change can never land in a scope its
// caller did not name.
func sameScope(w http.ResponseWriter, route, body string) bool {
	if route == body {
		return true
	}
	invalid(w, "this route changes the %s roster, and the request names %q", route, body)
	return false
}

func (h *handler) showRoster(w http.ResponseWriter, r *http.Request, scope string) {
	view, err := h.rosterOps().View(r.Context(), h.rosterTarget(scope))
	if err != nil {
		fail(w, err)
		return
	}
	out := api.RosterView{Scope: scope, InstallationVersion: view.Installation, ProjectVersion: view.Project, Roster: view.Resolved}
	if h.ModelClasses != nil {
		classes, err := h.ModelClasses(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		out.Models = map[string]map[string]string{}
		for _, agent := range view.Resolved.Agents {
			out.Models[agent.Name] = map[string]string{}
			for driver, mapping := range classes {
				out.Models[agent.Name][driver] = agent.ModelFor(driver, mapping)
			}
		}
	}
	reply(w, http.StatusOK, out)
}

func (h *handler) rosterHistory(w http.ResponseWriter, r *http.Request, scope string) {
	if asked := r.URL.Query().Get("scope"); asked != "" && !sameScope(w, scope, asked) {
		return
	}
	entries, err := h.rosterOps().History(r.Context(), h.rosterTarget(scope), r.URL.Query().Get("agent"))
	if err != nil {
		fail(w, err)
		return
	}
	out := api.RosterHistory{Scope: scope, Versions: make([]api.RosterEntry, 0, len(entries))}
	for _, entry := range entries {
		out.Versions = append(out.Versions, api.RosterEntry{Version: entry.Version, Difference: entry.Difference})
	}
	reply(w, http.StatusOK, out)
}

func (h *handler) changeRoster(w http.ResponseWriter, r *http.Request, scope string, actor ledger.Actor) {
	var in api.RosterChange
	if !decode(w, r, &in) || !sameScope(w, scope, in.Scope) {
		return
	}
	version, err := h.rosterOps().Change(r.Context(), h.rosterTarget(scope), in.Change, actor)
	if err != nil {
		failRoster(w, err)
		return
	}
	reply(w, http.StatusOK, version)
}

func (h *handler) revertRoster(w http.ResponseWriter, r *http.Request, scope string, actor ledger.Actor) {
	var in api.RosterRevert
	if !decode(w, r, &in) || !sameScope(w, scope, in.Scope) {
		return
	}
	if in.Version < 1 {
		invalid(w, "a revert names a version, from 1, and not %s", strconv.FormatInt(in.Version, 10))
		return
	}
	version, err := h.rosterOps().Revert(r.Context(), h.rosterTarget(scope), in.Version, actor)
	if err != nil {
		failRoster(w, err)
		return
	}
	reply(w, http.StatusOK, version)
}

// failRoster answers a roster error: a definition the format refuses is invalid, and a change that
// conflicts with what the roster holds is a conflict whose message names what.
func failRoster(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, roster.ErrInvalid), errors.Is(err, roster.ErrUnknownAgent), errors.Is(err, roster.ErrDisabledAgent):
		refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: err.Error()})
	case errors.Is(err, roster.ErrExists), errors.Is(err, roster.ErrNothingToReset), errors.Is(err, roster.ErrInUse):
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeRosterConflict, Message: err.Error()})
	default:
		fail(w, err)
	}
}
