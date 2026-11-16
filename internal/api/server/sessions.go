package server

import (
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

func (h *handler) sessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/sessions", h.signIn)
	mux.HandleFunc("POST /v1/sessions/end", h.member(h.signOut))
	mux.HandleFunc("POST /v1/sessions/end-all", h.member(h.signOutAll))
	mux.HandleFunc("GET /v1/delegates", h.member(h.listDelegates))
	mux.HandleFunc("POST /v1/delegates", h.member(h.createDelegate))
	mux.HandleFunc("POST /v1/delegates/{id}/revoke", h.member(h.revokeDelegate))
	mux.HandleFunc("POST /v1/delegates/{id}/remove", h.member(h.removeDelegate))
	mux.HandleFunc("GET "+api.OperationsPath, h.operations)
	mux.HandleFunc("GET /v1/passphrase", h.passphrase)
	mux.HandleFunc("PUT /v1/passphrase", h.setPassphrase)
}

// passphrase says whether one is set. A first run reads it so it asks for a passphrase only when
// none exists, and the answer gives nothing of the passphrase or its hash.
func (h *handler) passphrase(w http.ResponseWriter, r *http.Request) {
	set, err := h.sessions.Enrolled(r.Context(), h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.PassphraseState{Set: set})
}

func sessionOf(r *http.Request) string { return r.Header.Get(api.HeaderSession) }

func (h *handler) signIn(w http.ResponseWriter, r *http.Request) {
	var in api.SignIn
	if !decode(w, r, &in) {
		return
	}
	grant, err := h.sessions.SignIn(r.Context(), h.Scope, ports.Proof{Passphrase: in.Passphrase}, in.TTLMs)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, api.Session{Token: grant.Token, Member: grant.Member, ExpiresAt: grant.ExpiresAt})
}

func (h *handler) signOut(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	if err := h.sessions.End(r.Context(), h.Scope, sessionOf(r), actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) setPassphrase(w http.ResponseWriter, r *http.Request) {
	var in api.SetPassphrase
	if !decode(w, r, &in) {
		return
	}
	if err := h.sessions.Enroll(r.Context(), h.Scope, ports.Proof{Passphrase: in.Passphrase}, sessionOf(r)); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
