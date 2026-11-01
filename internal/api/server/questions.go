package server

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
)

func (h *handler) questionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/questions", h.listQuestions)
	mux.HandleFunc("POST /v1/questions", h.member(h.recordQuestion))
	mux.HandleFunc("GET /v1/questions/{id}", h.showQuestion)
	mux.HandleFunc("POST /v1/questions/{id}/dismiss", h.member(h.dismissQuestion))
	mux.HandleFunc("GET /v1/decisions", h.listDecisions)
	mux.HandleFunc("POST /v1/decisions", h.member(h.recordDecision))
	mux.HandleFunc("GET /v1/decisions/{id}", h.showDecision)
	mux.HandleFunc("GET /v1/categories", h.listCategories)
	mux.HandleFunc("POST /v1/categories", h.member(h.addCategory))
	mux.HandleFunc("GET /v1/candidates", h.candidates)
}

func filterOf(r *http.Request) ports.RecordFilter {
	query := r.URL.Query()
	return ports.RecordFilter{
		Category: query.Get("category"), Status: query.Get("status"), Scope: query.Get("scope"), Text: query.Get("text"),
	}
}

func (h *handler) listQuestions(w http.ResponseWriter, r *http.Request) {
	list := api.QuestionList{Questions: []questions.Question{}}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Questions().Questions(filterOf(r))
		if err == nil && rows != nil {
			list.Questions = rows
		}
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, list)
}

func (h *handler) recordQuestion(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.RecordQuestion
	if !decode(w, r, &in) {
		return
	}
	record, err := h.questions.RecordQuestion(r.Context(), h.Scope, questions.Question{
		ID: in.ID, Text: in.Text, Categories: in.Categories, Blocking: in.Blocking,
		SubjectKind: in.SubjectKind, SubjectID: in.SubjectID, AskedBy: actor.ID,
	}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, record)
}

func (h *handler) showQuestion(w http.ResponseWriter, r *http.Request) {
	var row questions.Question
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.Questions().Question(r.PathValue("id"))
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, row)
}

func (h *handler) dismissQuestion(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	if err := h.questions.Dismiss(r.Context(), h.Scope, r.PathValue("id"), actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) listDecisions(w http.ResponseWriter, r *http.Request) {
	list := api.DecisionList{Decisions: []questions.Decision{}}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Questions().Decisions(filterOf(r))
		if err == nil && rows != nil {
			list.Decisions = rows
		}
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, list)
}

func (h *handler) recordDecision(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.RecordDecision
	if !decode(w, r, &in) {
		return
	}
	record, err := h.questions.RecordDecision(r.Context(), h.Scope, questions.Decision{
		ID: in.ID, Status: in.Status, Statement: in.Statement, Rationale: in.Rationale, Citations: in.Citations,
		Scope: in.Scope, Reversibility: in.Reversibility, Categories: in.Categories, Supersedes: in.Supersedes,
		QuestionID: in.QuestionID,
	}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, record)
}

func (h *handler) showDecision(w http.ResponseWriter, r *http.Request) {
	var row questions.Decision
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.Questions().Decision(r.PathValue("id"))
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, row)
}

func (h *handler) listCategories(w http.ResponseWriter, r *http.Request) {
	list := api.CategoryList{Categories: questions.BuiltIn()}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		added, err := tx.Questions().Categories()
		list.Categories = append(list.Categories, added...)
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	slices.Sort(list.Categories)
	reply(w, http.StatusOK, list)
}

func (h *handler) addCategory(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.AddCategory
	if !decode(w, r, &in) {
		return
	}
	if err := h.questions.AddCategory(r.Context(), h.Scope, in.Name, actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) candidates(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Get("kinds") == "" {
		invalid(w, "candidates need the kinds to read")
		return
	}
	limit, err := strconv.Atoi(query.Get("limit"))
	if err != nil && query.Get("limit") != "" {
		invalid(w, "limit %q is not a number", query.Get("limit"))
		return
	}
	found, err := h.Matcher.Candidates(r.Context(), h.Scope, ports.MatchRequest{
		Text: query.Get("text"), Kinds: strings.Split(query.Get("kinds"), ","), Limit: limit,
	})
	if err != nil {
		fail(w, err)
		return
	}
	list := api.CandidateList{Candidates: make([]api.Candidate, 0, len(found))}
	for _, candidate := range found {
		list.Candidates = append(list.Candidates, api.Candidate{Kind: candidate.Kind, RecordID: candidate.RecordID, Score: candidate.Score})
	}
	reply(w, http.StatusOK, list)
}
