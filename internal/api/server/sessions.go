package server

import (
	"net/http"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

func (h *handler) sessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/sessions", h.signIn)
	mux.HandleFunc("POST /v1/sessions/end", h.member(h.signOut))
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

// member runs a handler for a signed-in member. The socket admits only the owning user
// (NFR-SEC-2), and a lane runs as that user, so reaching the socket proves nothing. A read
// needs no session, and everything a member changes passes through here (D26).
func (h *handler) member(next func(http.ResponseWriter, *http.Request, ledger.Actor)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, err := h.sessions.Member(r.Context(), h.Scope, sessionOf(r))
		if err != nil {
			fail(w, err)
			return
		}
		next(w, r, actor)
	}
}

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
