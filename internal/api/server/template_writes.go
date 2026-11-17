package server

import (
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

// templateWriteRoutes are a member's two routes for a worker's template write: asking for one, and reading how it
// stands. The worker's own route, which reports one, sits with the plane protocol and never on the web listener.
func (h *handler) templateWriteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/workers/{id}/template-writes", h.member(h.requestTemplateWrite))
	mux.HandleFunc("GET /v1/workers/{id}/template-writes/{request}", h.showTemplateWrite)
}

// requestTemplateWrite records a member's request and answers 202 with its view, which the member reads until
// the worker reports.
func (h *handler) requestTemplateWrite(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.TemplateWrite
	if !decode(w, r, &in) {
		return
	}
	view, err := h.templateWrites.Request(r.Context(), r.PathValue("id"), in, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusAccepted, view)
}

func (h *handler) showTemplateWrite(w http.ResponseWriter, r *http.Request) {
	view, err := h.templateWrites.View(r.Context(), r.PathValue("id"), r.PathValue("request"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}

// workerTemplateReport records what a worker did with a request. The worker proves itself with its credential.
func (h *handler) workerTemplateReport(w http.ResponseWriter, r *http.Request) {
	var in ledger.TemplateWriteReport
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	if err := h.plane.ReportTemplateWrite(r.Context(), in); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
