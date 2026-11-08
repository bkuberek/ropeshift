package server

import (
	"errors"
	"net/http"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

func isNotFound(err error) bool { return errors.Is(err, ports.ErrNotFound) }

func (h *handler) effectRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/effects", h.listEffects)
	mux.HandleFunc("GET /v1/effects/{id}", h.showEffect)
	mux.HandleFunc("POST /v1/effects/{id}/decide", h.member(h.decideEffect))
}

func (h *handler) listEffects(w http.ResponseWriter, r *http.Request) {
	rows, err := h.effects.List(r.Context(), h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	if rows == nil {
		rows = []ledger.UnknownEffect{}
	}
	reply(w, http.StatusOK, api.EffectList{Effects: rows})
}

func (h *handler) showEffect(w http.ResponseWriter, r *http.Request) {
	row, err := h.effects.Get(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, row)
}

// decideEffect is a person's word on an effect nothing can observe. The session this route demands is
// the one thing a lane cannot present.
func (h *handler) decideEffect(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.DecideEffect
	if !decode(w, r, &in) {
		return
	}
	record, err := h.effects.Decide(r.Context(), h.Scope, ledger.EffectDecision{ID: r.PathValue("id"), Outcome: in.Outcome, Reason: in.Reason}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, record)
}
