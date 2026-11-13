package server

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// searchRequest is what a search asks: the words, one kind or every kind, and how many records.
type searchRequest struct {
	words []string
	query string
	kind  string
	limit int
	// member is who searches, whose own conversations are among what a search finds.
	member string
}

// searchOf reads a search from its query: q, the words; category, one kind; and limit, which a search with
// words takes from SearchLimit when it names none.
func searchOf(r *http.Request) (searchRequest, error) {
	values := r.URL.Query()
	query := strings.Join(strings.Fields(values.Get("q")), " ")
	request := searchRequest{words: strings.Fields(strings.ToLower(query)), query: query, kind: values.Get("category")}
	if text := values.Get("limit"); text != "" {
		limit, err := strconv.Atoi(text)
		if err != nil || limit < 1 {
			return request, fmt.Errorf("limit %q is not a positive number", text)
		}
		request.limit = limit
	} else if len(request.words) > 0 {
		request.limit = api.SearchLimit
	}
	return request, nil
}

// rank says how well a record holds the words, the lower the better, and whether it holds them at all.
// An exact ID ranks first, then an ID that begins with the words, then words that each begin a word of
// the ID or title, then words found anywhere in them.
func (s searchRequest) rank(record api.SearchRecord) (int, bool) {
	if len(s.words) == 0 {
		return 0, true
	}
	id, text := strings.ToLower(record.ID), strings.ToLower(record.ID+" "+record.Title)
	for _, word := range s.words {
		if !strings.Contains(text, word) {
			return 0, false
		}
	}
	whole := strings.Join(s.words, " ")
	switch {
	case id == whole:
		return 0, true
	case strings.HasPrefix(id, whole):
		return 1, true
	}
	starts := strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == '-' || r == '_' || r == '/' || r == '.' || r == ':' })
	for _, word := range s.words {
		if !slices.ContainsFunc(starts, func(part string) bool { return strings.HasPrefix(part, word) }) {
			return 3, true
		}
	}
	return 2, true
}

// serveSearch refuses a request it cannot read before it reads the ledger.
func (h *handler) serveSearch(w http.ResponseWriter, r *http.Request) {
	request, err := searchOf(r)
	if err != nil {
		invalid(w, "%v", err)
		return
	}
	request.member = h.viewer(r)
	h.serveView(w, r, func(tx ports.ReadTx, _ *http.Request, stamp api.Stamp) (any, error) {
		return h.readSearch(tx, request, stamp)
	})
}

func (h *handler) readSearch(tx ports.ReadTx, request searchRequest, stamp api.Stamp) (any, error) {
	every, err := h.searchable(tx, request.kind)
	if err != nil {
		return nil, err
	}
	if request.member != "" && (request.kind == "" || request.kind == api.SearchConversation) {
		owned, err := tx.Conversations().OfMember(request.member)
		if err != nil {
			return nil, err
		}
		for _, record := range owned {
			every = append(every, api.SearchRecord{Kind: api.SearchConversation, ID: record.ID, Title: record.Name, State: string(record.Actual)})
		}
	}
	type found struct {
		record api.SearchRecord
		rank   int
		order  int
	}
	var hits []found
	for _, record := range every {
		if rank, ok := request.rank(record); ok {
			hits = append(hits, found{record: record, rank: rank, order: slices.Index(api.SearchKinds, record.Kind)})
		}
	}
	// Ties go by title before ID, so the order never rests on an ID the ledger made at random.
	slices.SortStableFunc(hits, func(a, b found) int {
		return cmp.Or(cmp.Compare(a.rank, b.rank), cmp.Compare(a.order, b.order), cmp.Compare(a.record.Title, b.record.Title), cmp.Compare(a.record.ID, b.record.ID))
	})
	view := api.Search{Stamp: stamp, Query: request.query, Records: make([]api.SearchRecord, 0, len(hits))}
	for _, hit := range hits {
		if request.limit > 0 && len(view.Records) == request.limit {
			view.More = true
			break
		}
		view.Records = append(view.Records, hit.record)
	}
	return view, nil
}

// searchable lists the project's records of one kind, or of every kind, with the newest version of each.
func (h *handler) searchable(tx ports.ReadTx, kind string) ([]api.SearchRecord, error) {
	var out []api.SearchRecord
	add := func(kindOf, id, title, state string) {
		if kind == "" || kind == kindOf {
			out = append(out, api.SearchRecord{Kind: kindOf, ID: id, Title: title, State: state})
		}
	}
	wants := func(kindOf string) bool { return kind == "" || kind == kindOf }
	if wants(api.SearchWorkOrder) || wants(api.SearchLane) {
		orders, err := tx.WorkOrders().List()
		if err != nil {
			return nil, err
		}
		titles := map[string]string{}
		for _, order := range orders {
			titles[order.ID] = order.Title
			add(api.SearchWorkOrder, order.ID, order.Title, string(order.State))
		}
		lanes, err := h.workers.LanesIn(tx)
		if err != nil {
			return nil, err
		}
		// A lane's ID says nothing to a person, so a lane is found by the title of the work it runs.
		for _, lane := range lanes {
			add(api.SearchLane, lane.ID, cmp.Or(titles[lane.WorkOrder], lane.WorkOrder), lane.ActualState)
		}
	}
	if err := searchPlanning(tx, wants, add); err != nil {
		return nil, err
	}
	if wants(api.SearchDecision) {
		decisions, err := allDecisions(tx, ports.RecordFilter{})
		if err != nil {
			return nil, err
		}
		for _, decision := range decisions {
			add(api.SearchDecision, decision.ID, decision.Statement, string(decision.Status))
		}
	}
	if wants(api.SearchQuestion) {
		every, err := allQuestions(tx)
		if err != nil {
			return nil, err
		}
		for _, question := range every {
			add(api.SearchQuestion, question.ID, question.Text, string(question.Status))
		}
	}
	if wants(api.SearchRepository) {
		repositories, err := tx.Repositories().Repositories()
		if err != nil {
			return nil, err
		}
		for _, repository := range repositories {
			add(api.SearchRepository, repository.ID, repository.Name, string(cmp.Or(repository.State, ledger.RepositoryActive)))
		}
	}
	return out, nil
}

// searchPlanning lists plans, the newest version of each contract and requirement, and the planning
// documents the ledger recorded.
func searchPlanning(tx ports.ReadTx, wants func(string) bool, add func(kind, id, title, state string)) error {
	planning := tx.Planning()
	if wants(api.SearchPlan) {
		plans, err := planning.Plans()
		if err != nil {
			return err
		}
		for _, plan := range plans {
			add(api.SearchPlan, plan.ID, plan.Title, plan.Status)
		}
	}
	if wants(api.SearchContract) {
		contracts, err := planning.Contracts()
		if err != nil {
			return err
		}
		newest := map[string]int{}
		for at, contract := range contracts {
			if was, ok := newest[contract.ID]; !ok || contracts[was].Version < contract.Version {
				newest[contract.ID] = at
			}
		}
		for at, contract := range contracts {
			if newest[contract.ID] == at {
				add(api.SearchContract, contract.ID, contract.Title, "")
			}
		}
	}
	if wants(api.SearchRequirement) {
		requirements, err := planning.Requirements()
		if err != nil {
			return err
		}
		newest := map[string]int{}
		for at, requirement := range requirements {
			if was, ok := newest[requirement.Key]; !ok || requirements[was].Version < requirement.Version {
				newest[requirement.Key] = at
			}
		}
		for at, requirement := range requirements {
			if newest[requirement.Key] == at {
				add(api.SearchRequirement, requirement.Key, requirement.Text, requirement.Status)
			}
		}
	}
	if wants(api.SearchDocument) {
		documents, err := planning.Documents()
		if err != nil {
			return err
		}
		for _, document := range documents {
			add(api.SearchDocument, document.ID, document.Title, string(document.Kind))
		}
	}
	return nil
}

// allQuestions reads every question, answered or not, a page at a time.
func allQuestions(tx ports.ReadTx) ([]questions.Question, error) {
	var out []questions.Question
	filter := ports.RecordFilter{}
	for {
		page, err := tx.Questions().Questions(filter)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < ports.MaxRecords {
			return out, nil
		}
		filter.After = page[len(page)-1].ID
	}
}

// viewer is the member a read is for: a member's session, or the member a delegate acts for. A read with
// neither reads as nobody's, and finds no conversation.
func (h *handler) viewer(r *http.Request) string {
	if credential, ok := delegateOf(r.Context()); ok {
		return credential.Member
	}
	actor, err := h.sessions.Member(r.Context(), h.Scope, sessionOf(r))
	if err != nil {
		return ""
	}
	return actor.ID
}
