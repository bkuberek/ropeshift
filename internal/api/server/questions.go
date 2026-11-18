package server

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

func (h *handler) questionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/questions", h.listQuestions)
	mux.HandleFunc("POST /v1/questions", h.member(h.recordQuestion))
	mux.HandleFunc("GET /v1/questions/{id}", h.showQuestion)
	mux.HandleFunc("POST /v1/questions/{id}/dismiss", h.member(h.dismissQuestion))
	mux.HandleFunc("POST /v1/questions/{id}/answer", h.member(h.answerQuestion))
	mux.HandleFunc("GET /v1/question-batches", h.listBatches)
	mux.HandleFunc("POST /v1/batches/{id}/answers", h.member(h.answerBatch))
	mux.HandleFunc("GET /v1/decisions", h.listDecisions)
	mux.HandleFunc("POST /v1/decisions", h.member(h.recordDecision))
	mux.HandleFunc("GET /v1/decisions/{id}", h.showDecision)
	mux.HandleFunc("POST /v1/decisions/{id}/overturn", h.member(h.overturnDecision))
	mux.HandleFunc("GET /v1/policies", h.listPolicies)
	mux.HandleFunc("POST /v1/policies", h.member(h.publishPolicy))
	mux.HandleFunc("GET /v1/policies/{version}", h.showPolicy)
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
	list := api.QuestionList{Questions: []api.QuestionView{}}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Questions().Questions(filterOf(r))
		if err != nil {
			return err
		}
		list.Questions, err = questionViews(tx, rows)
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
	var view api.QuestionView
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		row, err := tx.Questions().Question(r.PathValue("id"))
		if err != nil {
			return err
		}
		views, err := questionViews(tx, []questions.Question{row})
		if err == nil {
			view = views[0]
		}
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}

// questionViews answers questions with the scope an answer to each takes, from the record it is about, and the
// title of that record, read in the read that read them, once for each kind of scope they hold.
func questionViews(tx ports.ReadTx, rows []questions.Question) ([]api.QuestionView, error) {
	views := make([]api.QuestionView, 0, len(rows))
	scopes := make([]string, 0, len(rows))
	for _, row := range rows {
		scope := questions.ScopeOf(row.SubjectKind, row.SubjectID)
		views = append(views, api.QuestionView{Question: row, Scope: scope})
		scopes = append(scopes, scope)
	}
	titles, err := control.ScopeTitles(tx, scopes)
	if err != nil {
		return nil, err
	}
	for i := range views {
		views[i].ScopeTitle = titles[views[i].Scope]
	}
	return views, nil
}

// questionBatches answers presented batches with the title of the record each item's scope names, read once for
// each kind of scope every batch holds.
func questionBatches(tx ports.ReadTx, presented []questions.Presented) ([]api.QuestionBatch, error) {
	var scopes []string
	for _, batch := range presented {
		for _, item := range batch.Items {
			scopes = append(scopes, item.Scope)
		}
	}
	titles, err := control.ScopeTitles(tx, scopes)
	if err != nil {
		return nil, err
	}
	out := make([]api.QuestionBatch, 0, len(presented))
	for _, batch := range presented {
		items := make([]api.QuestionItem, 0, len(batch.Items))
		for _, item := range batch.Items {
			items = append(items, api.QuestionItem{Item: item, ScopeTitle: titles[item.Scope]})
		}
		out = append(out, api.QuestionBatch{Batch: batch.Batch, Items: items})
	}
	return out, nil
}

func (h *handler) dismissQuestion(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	if err := h.questions.Dismiss(r.Context(), h.Scope, r.PathValue("id"), actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) answerQuestion(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.AnswerQuestion
	if !decode(w, r, &in) {
		return
	}
	channel, ok := placed(w, r, in.Place)
	if !ok {
		return
	}
	record, err := h.questions.AnswerFrom(r.Context(), h.Scope, r.PathValue("id"), questions.Answer{
		DecisionID: in.ID, Statement: in.Answer, Rationale: in.Rationale, Citations: in.Citations, Supersedes: in.Supersedes, NoConflict: in.NoConflict,
	}, actor, channel)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, record)
}

// answerBatch records every answer of a request or none. A member's answer of their own words says why, and one
// that accepts a recommendation takes its rationale and the item's citations.
func (h *handler) answerBatch(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.AnswerBatch
	if !decode(w, r, &in) {
		return
	}
	channel, ok := placed(w, r, in.Place)
	if !ok {
		return
	}
	answers := make([]questions.BatchAnswer, 0, len(in.Answers))
	for _, item := range in.Answers {
		answers = append(answers, questions.BatchAnswer{
			Question: item.Question, Accepted: item.Accepted,
			Answer: questions.Answer{
				DecisionID: item.ID, Statement: item.Answer, Rationale: item.Rationale, Citations: item.Citations,
				Supersedes: item.Supersedes, NoConflict: item.NoConflict,
			},
		})
	}
	decisions, err := h.questions.AnswerBatchFrom(r.Context(), h.Scope, r.PathValue("id"), answers, actor, channel)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, api.BatchAnswered{Decisions: decisions})
}

// listBatches is what reached members and still waits for one. A member filters by
// category, and nothing is assigned to anyone.
func (h *handler) listBatches(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	list := api.BatchList{Batches: []api.QuestionBatch{}}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		batches, err := tx.Questions().WaitingBatches()
		if err != nil {
			return err
		}
		policy, err := control.DecisionPolicy(tx)
		if err != nil {
			return err
		}
		var waiting []questions.Presented
		for _, batch := range batches {
			rows, err := tx.Questions().BatchQuestions(batch.ID)
			if err != nil {
				return err
			}
			if presented := questions.Present(batch, rows, category, policy); len(presented.Items) != 0 {
				waiting = append(waiting, presented)
			}
		}
		list.Batches, err = questionBatches(tx, waiting)
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, list)
}

func (h *handler) listDecisions(w http.ResponseWriter, r *http.Request) {
	list := api.DecisionList{Decisions: []api.DecisionView{}}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Questions().Decisions(filterOf(r))
		if err != nil {
			return err
		}
		list.Decisions, err = decisionViews(tx, rows)
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
	if _, err := questions.ParseScope(in.Scope); err != nil {
		fail(w, err)
		return
	}
	record, err := h.questions.RecordDecision(r.Context(), h.Scope, questions.Decision{
		ID: in.ID, Status: in.Status, Statement: in.Statement, Rationale: in.Rationale, Citations: in.Citations,
		Scope: in.Scope, Reversibility: in.Reversibility, Categories: in.Categories, Supersedes: in.Supersedes,
		QuestionID: in.QuestionID, NoConflict: in.NoConflict,
	}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, record)
}

func (h *handler) showDecision(w http.ResponseWriter, r *http.Request) {
	var view api.DecisionView
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		row, err := tx.Questions().Decision(r.PathValue("id"))
		if err != nil {
			return err
		}
		views, err := decisionViews(tx, []questions.Decision{row})
		if err == nil {
			view = views[0]
		}
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}

// decisionViews answers decisions with the title of the record each one's scope names, read in the read that
// read them, once for each kind of scope they hold.
func decisionViews(tx ports.ReadTx, rows []questions.Decision) ([]api.DecisionView, error) {
	titles, err := decisionScopeTitles(tx, rows)
	if err != nil {
		return nil, err
	}
	views := make([]api.DecisionView, 0, len(rows))
	for _, row := range rows {
		views = append(views, api.DecisionView{Decision: row, ScopeTitle: titles[row.Scope]})
	}
	return views, nil
}

// decisionScopeTitles names the record each decision's scope names, by the scope.
func decisionScopeTitles(tx ports.ReadTx, rows []questions.Decision) (map[string]string, error) {
	scopes := make([]string, 0, len(rows))
	for _, row := range rows {
		scopes = append(scopes, row.Scope)
	}
	return control.ScopeTitles(tx, scopes)
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
