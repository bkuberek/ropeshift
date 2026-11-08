package server

import (
	"fmt"
	"net/http"

	"github.com/BurntSushi/toml"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/ledger"
)

// autonomyTable names the table of an autonomy policy.
const autonomyTable = "autonomy_policy"

// readAutonomy reads a body as its author wrote it. A key left out reads as zero, which the policy's
// own check refuses, so no bound loosens by being forgotten.
func readAutonomy(body string) (authority.AutonomyDraft, error) {
	var file authority.AutonomyPolicyFile
	meta, err := toml.Decode(body, &file)
	if err != nil {
		return authority.AutonomyDraft{}, fmt.Errorf("%w: %v", authority.ErrBadLanePolicy, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return authority.AutonomyDraft{}, fmt.Errorf("%w: unknown setting %s", authority.ErrBadLanePolicy, unknown[0])
	}
	return authority.AutonomyDraft{Body: body, Policy: file.AutonomyPolicy}, nil
}

// autonomyRoutes keep every change behind a member's session. Reaching the socket proves nothing,
// because a lane runs as the same user, and these are what hold automated use near a person.
func (h *handler) autonomyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+api.RiskPath, h.showRisk)
	mux.HandleFunc("GET /v1/workers/{id}/autonomy", h.showAutonomy)
	mux.HandleFunc("PUT /v1/workers/{id}/autonomy", h.member(h.setAutonomy))
	mux.HandleFunc("POST /v1/workers/{id}/autonomy/acknowledgement", h.member(h.acknowledgeRisk))
	mux.HandleFunc("POST /v1/workers/{id}/windows", h.member(h.openWindow))
	mux.HandleFunc("DELETE /v1/workers/{id}/windows", h.member(h.closeWindow))
}

func (h *handler) showRisk(w http.ResponseWriter, _ *http.Request) {
	reply(w, http.StatusOK, api.RiskStatement{Statement: authority.RiskStatement, Digest: authority.RiskDigest()})
}

func (h *handler) showAutonomy(w http.ResponseWriter, r *http.Request) {
	view, err := h.autonomy.View(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}

func (h *handler) setAutonomy(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.SetAutonomy
	if !decode(w, r, &in) {
		return
	}
	if err := h.autonomy.SetLevel(r.Context(), h.Scope, r.PathValue("id"), in.Level, actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) acknowledgeRisk(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.AcknowledgeRisk
	if !decode(w, r, &in) {
		return
	}
	if err := h.autonomy.AcknowledgeRisk(r.Context(), h.Scope, r.PathValue("id"), in.Digest, actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) openWindow(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.OpenWindow
	if !decode(w, r, &in) {
		return
	}
	row, err := h.autonomy.OpenWindow(r.Context(), h.Scope, r.PathValue("id"), in.ID, in.LengthMs, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, row)
}

func (h *handler) closeWindow(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	if err := h.autonomy.CloseWindow(r.Context(), h.Scope, r.PathValue("id"), actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
