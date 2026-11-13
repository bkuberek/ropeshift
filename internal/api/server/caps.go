package server

import (
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// capsRoutes read a worker's lane caps, and set them behind a member's session: they protect the machine
// and the sign-in, which are a person's (FR-PROJ-7).
func (h *handler) capsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/workers/{id}/caps", h.showCaps)
	mux.HandleFunc("PUT /v1/workers/{id}/caps", h.member(h.setCaps))
}

func (h *handler) showCaps(w http.ResponseWriter, r *http.Request) {
	view, err := h.caps.View(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}

func (h *handler) setCaps(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.SetLaneCaps
	if !decode(w, r, &in) {
		return
	}
	view, err := h.caps.Set(r.Context(), r.PathValue("id"), lanes.LaneCapSettings{MaxLanes: in.MaxLanes, MaxLanesPerSignIn: in.MaxLanesPerSignIn}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}
