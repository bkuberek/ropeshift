package server

import (
	"context"
	"errors"
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// repositoryRoutes keep every change behind a member's session. How much ropeshift may merge, and what
// dependent work starts from, are the person's setting and nobody else's (FR-REPO-1).
func (h *handler) repositoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/repositories", h.listRepositories)
	mux.HandleFunc("GET /v1/repositories/{id}", h.showRepository)
	mux.HandleFunc("PATCH /v1/repositories/{id}", h.member(h.changeRepository))
	mux.HandleFunc("POST /v1/repositories", h.member(h.addRepositories))
	mux.HandleFunc("POST /v1/repositories/{id}/remove", h.member(h.removeRepository))
}

// removeRepository takes a repository out of its project once no open work targets it (ADR-0021). The
// worker this daemon runs forgets its checkout.
func (h *handler) removeRepository(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	record, err := h.repositories.Resolve(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	removed, err := h.repositories.Remove(r.Context(), h.Scope, record.ID, h.Checkouts, actor)
	if err != nil {
		fail(w, err)
		return
	}
	h.replyRepository(r.Context(), w, removed)
}

// replyRepository answers with one record as a client reads it.
func (h *handler) replyRepository(ctx context.Context, w http.ResponseWriter, record ledger.Repository) {
	agreement, err := h.repositories.ReviewAgreement(ctx, h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	plan, err := h.planOfRecord(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	view := api.ViewOf(record, agreement)
	view.PlanOfRecord = record.ID == plan
	reply(w, http.StatusOK, view)
}

// planOfRecord names the repository the project plans in, and is empty for a project that has none.
func (h *handler) planOfRecord(ctx context.Context) (string, error) {
	record, err := h.repositories.Planning(ctx, h.Scope)
	if errors.Is(err, ports.ErrNotFound) {
		return "", nil
	}
	return record.ID, err
}

// addRepositories registers the repositories of clones on the machine of the worker this daemon runs
// (ADR-0022). The answer holds every clone's outcome, a refusal among them, so a client says which.
func (h *handler) addRepositories(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.RepositoryAdd
	if !decode(w, r, &in) {
		return
	}
	if h.Checkouts == nil {
		refuse(w, http.StatusNotImplemented, api.ErrorDetail{Code: api.CodeUnavailable, Message: "this daemon runs no worker whose clones it can read"})
		return
	}
	added, err := h.repositories.Add(r.Context(), h.Scope, control.RepositoryAdd{Paths: in.Paths, Depth: in.Depth, DryRun: in.DryRun}, h.Checkouts, actor)
	if err != nil {
		fail(w, err)
		return
	}
	report := api.RepositoryAddReport{DryRun: in.DryRun, Repositories: make([]api.RepositoryAdded, 0, len(added))}
	for _, one := range added {
		view := api.RepositoryAdded{Path: one.Path, Outcome: one.Outcome, Checkout: one.Checkout, Reason: one.Reason}
		if one.Repository.ID != "" {
			record := one.Repository
			view.Repository = &record
		}
		report.Repositories = append(report.Repositories, view)
	}
	reply(w, http.StatusOK, report)
}

func (h *handler) listRepositories(w http.ResponseWriter, r *http.Request) {
	records, err := h.repositories.List(r.Context(), h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	agreement, err := h.repositories.ReviewAgreement(r.Context(), h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	plan, err := h.planOfRecord(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	list := api.RepositoryList{Repositories: make([]api.RepositoryView, 0, len(records))}
	for _, record := range records {
		view := api.ViewOf(record, agreement)
		view.PlanOfRecord = record.ID == plan
		list.Repositories = append(list.Repositories, view)
	}
	reply(w, http.StatusOK, list)
}

func (h *handler) showRepository(w http.ResponseWriter, r *http.Request) {
	record, err := h.repositories.Resolve(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	h.replyRepository(r.Context(), w, record)
}

func (h *handler) changeRepository(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.RepositoryChange
	if !decode(w, r, &in) {
		return
	}
	record, err := h.repositories.Resolve(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	changed, err := h.repositories.Change(r.Context(), h.Scope, record.ID, control.RepositoryChange{
		Name: in.Name, Base: in.Base, MergeMethod: in.MergeMethod, StartFrom: in.StartFrom, ReviewTarget: in.ReviewTarget, MergeAuthority: in.MergeAuthority,
		Planning: in.Planning,
	}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	h.replyRepository(r.Context(), w, changed)
}
