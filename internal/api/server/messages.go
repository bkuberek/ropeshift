package server

import (
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
)

func (h *handler) messageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/messages", h.member(h.sendMessage))
	mux.HandleFunc("GET /v1/messages/{id}", h.showMessage)
	mux.HandleFunc("GET /v1/lanes/{id}/messages", h.listMessages)
}

func (h *handler) sendMessage(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in messages.Draft
	if !decode(w, r, &in) {
		return
	}
	record, err := h.messages.Send(r.Context(), h.Scope, in, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, record)
}

// showMessage is how a sender learns what became of a message.
func (h *handler) showMessage(w http.ResponseWriter, r *http.Request) {
	record, err := h.messages.ReadMessage(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, record)
}

func (h *handler) listMessages(w http.ResponseWriter, r *http.Request) {
	rows, err := h.messages.LaneMessages(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if rows == nil {
		rows = []messages.Message{}
	}
	reply(w, http.StatusOK, api.MessageList{Messages: rows})
}
