package server

import (
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

// templateWriteRoutes are a member's routes for a worker's template write and forge account read: asking for one, and
// reading how it stands. Each read is a member's alone, as each request is, because the socket admits whoever runs as
// the owning user and a forge account read names a GitHub login and its organizations. The worker's own routes, which
// report one, sit with the plane protocol and never on the web listener.
func (h *handler) templateWriteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/workers/{id}/template-writes", h.member(h.requestTemplateWrite))
	mux.HandleFunc("GET /v1/workers/{id}/template-writes/{request}", h.member(h.showTemplateWrite))
	mux.HandleFunc("POST /v1/workers/{id}/forge-account-reads", h.member(h.requestForgeAccountRead))
	mux.HandleFunc("GET /v1/workers/{id}/forge-account-reads/{request}", h.member(h.showForgeAccountRead))
}

// requestForgeAccountRead records a member's request that a worker read its forge account and answers 202 with its
// view, which the member reads until the worker reports.
func (h *handler) requestForgeAccountRead(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in ledger.ForgeAccountRequest
	if !decode(w, r, &in) {
		return
	}
	view, err := h.forgeAccountReads.Request(r.Context(), r.PathValue("id"), in, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusAccepted, view)
}

func (h *handler) showForgeAccountRead(w http.ResponseWriter, r *http.Request, _ ledger.Actor) {
	view, err := h.forgeAccountReads.View(r.Context(), r.PathValue("id"), r.PathValue("request"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}

// workerForgeAccountReport records what a worker found of its forge account. The worker proves itself with its
// credential.
func (h *handler) workerForgeAccountReport(w http.ResponseWriter, r *http.Request) {
	var in ledger.ForgeAccountReport
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	if err := h.plane.ReportForgeAccount(r.Context(), in); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// workerPracticesReport records what a worker did with a request to run the practices check. The worker proves itself
// with its credential.
func (h *handler) workerPracticesReport(w http.ResponseWriter, r *http.Request) {
	var in ledger.PracticesReport
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	if err := h.plane.ReportPracticesCheck(r.Context(), in); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (h *handler) showTemplateWrite(w http.ResponseWriter, r *http.Request, _ ledger.Actor) {
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
