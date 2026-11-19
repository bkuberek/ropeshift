package server

import (
	"fmt"
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
)

// practicesRoutes read the practices check's results, and ask that the check run. A request changes no state a
// decision relies on, and a finding blocks nothing, so the read needs no session beyond the project's.
func (h *handler) practicesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/practices", h.listPractices)
	mux.HandleFunc("POST /v1/practices/checks", h.member(h.checkPractices))
	mux.HandleFunc("GET /v1/practices/checks/{request}", h.showPracticesCheck)
	mux.HandleFunc("POST /v1/practices/acknowledgements", h.member(h.acknowledgePractices))
	mux.HandleFunc("POST /v1/practices/acknowledgements/withdraw", h.member(h.withdrawPractices))
}

// practicesNamed resolves the repositories a request or a read names, each by identity, name, or prefix of its
// identity, to the identities the use cases take.
func (h *handler) practicesNamed(r *http.Request, names []string) ([]string, error) {
	ids := make([]string, 0, len(names))
	for _, name := range names {
		record, err := h.repositories.Resolve(r.Context(), h.Scope, name)
		if err != nil {
			return nil, err
		}
		ids = append(ids, record.ID)
	}
	return ids, nil
}

// listPractices answers the latest result and the open request of each active repository, or of the one named.
func (h *handler) listPractices(w http.ResponseWriter, r *http.Request) {
	var names []string
	if name := r.URL.Query().Get("repository"); name != "" {
		names = []string{name}
	}
	ids, err := h.practicesNamed(r, names)
	if err != nil {
		fail(w, err)
		return
	}
	reads, err := h.practices.Read(r.Context(), h.Scope, ids...)
	if err != nil {
		fail(w, err)
		return
	}
	workers, err := h.practices.WorkerNames(r.Context(), h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	list := api.PracticesList{Repositories: make([]api.PracticesView, 0, len(reads))}
	for _, read := range reads {
		list.Repositories = append(list.Repositories, api.PracticesViewOf(read.Repository, read.Result, read.Open, read.Acknowledgements, read.Now, workers))
	}
	reply(w, http.StatusOK, list)
}

// checkPractices asks the worker that keeps each repository's checkout to run the check. The answer is 202 with one
// entry for each repository: the request, or why none was made.
func (h *handler) checkPractices(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.PracticesCheck
	if !decode(w, r, &in) {
		return
	}
	ids, err := h.practicesNamed(r, in.Repositories)
	if err != nil {
		fail(w, err)
		return
	}
	asked, err := h.practices.Request(r.Context(), h.Scope, ids, ledger.PracticesMember, actor)
	if err != nil {
		fail(w, err)
		return
	}
	workers, err := h.practices.WorkerNames(r.Context(), h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusAccepted, practicesChecked(asked, workers))
}

// practicesChecked is what a client reads of each repository a request asked of.
func practicesChecked(asked []control.PracticesAsked, workers map[string]string) api.PracticesChecked {
	out := api.PracticesChecked{Checks: make([]api.PracticesCheckEntry, 0, len(asked))}
	for _, one := range asked {
		entry := api.PracticesCheckEntry{Repository: one.Repository.ID, Name: one.Repository.Name, Existing: one.Existing, Reason: one.Reason}
		if one.Request != nil {
			request := api.PracticesRequestViewOf(*one.Request, 0, workers)
			entry.Request = &request
		}
		out.Checks = append(out.Checks, entry)
	}
	return out
}

// showPracticesCheck answers one request, with the state it stands in.
func (h *handler) showPracticesCheck(w http.ResponseWriter, r *http.Request) {
	request, err := h.practices.Check(r.Context(), h.Scope, r.PathValue("request"))
	if err != nil {
		fail(w, err)
		return
	}
	workers, err := h.practices.WorkerNames(r.Context(), h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.PracticesRequestViewOf(request, 0, workers))
}

// acknowledgedRepository resolves the repository an acknowledgement names, by name, identity, or prefix of it, to its
// identity.
func (h *handler) acknowledgedRepository(r *http.Request, name string) (string, error) {
	ids, err := h.practicesNamed(r, []string{name})
	if err != nil {
		return "", err
	}
	return ids[0], nil
}

// acknowledgePractices records a member's reason for leaving a finding open. The finding stays in every view of the
// result, marked, and nothing that decides anything reads it.
func (h *handler) acknowledgePractices(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.PracticesAcknowledge
	if !decode(w, r, &in) {
		return
	}
	repository, err := h.acknowledgedRepository(r, in.Repository)
	if err != nil {
		fail(w, err)
		return
	}
	ack, err := h.practices.Acknowledge(r.Context(), h.Scope, repository, in.Finding, in.Reason, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, api.PracticesAcknowledgementOf(ack))
}

// withdrawPractices removes the acknowledgement of a finding, which reads as open again, and answers what it removed.
func (h *handler) withdrawPractices(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.PracticesWithdraw
	if !decode(w, r, &in) {
		return
	}
	repository, err := h.acknowledgedRepository(r, in.Repository)
	if err != nil {
		fail(w, err)
		return
	}
	ack, err := h.practices.Withdraw(r.Context(), h.Scope, repository, in.Finding, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.PracticesAcknowledgementOf(ack))
}

// requestPlanningChecks asks for a check of every active repository when a planning session's work order is recorded,
// so the session's brief can hold a result. A planning session never fails for it: a request that is refused with a
// reason, as every request is when no worker keeps a checkout, and one that fails, leave the work order as it was
// recorded, and a failure is written to the server's log.
func (h *handler) requestPlanningChecks(r *http.Request, actor ledger.Actor) {
	if _, err := h.practices.Request(r.Context(), h.Scope, nil, ledger.PracticesPlanningSession, actor); err != nil && h.Warn != nil {
		h.Warn(fmt.Sprintf("the practices check of the project's repositories was not requested when a planning session started: %v", err))
	}
}
