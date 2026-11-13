package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// conversationRoutes are a project's routes of the conversation surface: the member's conversations with
// the assistant, what the member can talk to, and the streams of a lane and of an orchestrator.
func (h *handler) conversationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/conversations", h.reader(h.listConversations))
	mux.HandleFunc("POST /v1/conversations", h.member(h.createConversation))
	mux.HandleFunc("GET /v1/conversations/{id}", h.reader(h.showConversation))
	mux.HandleFunc("POST /v1/conversations/{id}/open", h.member(h.openConversation))
	mux.HandleFunc("POST /v1/conversations/{id}/stop", h.member(h.stopConversation))
	mux.HandleFunc("POST /v1/conversations/{id}/rename", h.member(h.renameConversation))
	mux.HandleFunc("POST /v1/conversations/{id}/delete", h.member(h.deleteConversation))
	mux.HandleFunc("POST /v1/conversations/{id}/compact", h.member(h.compactConversation))
	mux.HandleFunc("POST /v1/conversations/{id}/clear", h.member(h.clearConversation))
	mux.HandleFunc("GET /v1/conversations/{id}/entries", h.reader(h.conversationEntries))
	mux.HandleFunc("POST /v1/conversations/{id}/messages", h.member(h.messageAssistant))
	mux.HandleFunc("GET "+api.ConversationTargetsPath, h.reader(h.conversationTargets))
	mux.HandleFunc("GET /v1/lanes/{id}/stream", h.reader(h.laneStream))
	mux.HandleFunc("GET /v1/orchestrators/{worker}/stream", h.reader(h.orchestratorStream))
}

// reader runs a read for the principal a request proves: a member's live session, or a delegate credential
// the gate admitted, which reads as its member does. A read changes nothing, so no class applies.
func (h *handler) reader(next func(http.ResponseWriter, *http.Request, ledger.Actor)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, _, ok := h.admit(w, r)
		if !ok {
			return
		}
		next(w, r, actor)
	}
}

// workerNames names each registered worker by its ID.
func (h *handler) workerNames(ctx context.Context) (map[string]string, error) {
	workers, err := h.plane.List(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(workers))
	for _, worker := range workers {
		names[worker.ID] = worker.Name
	}
	return names, nil
}

// conversationView is a conversation as the API answers it.
func (h *handler) conversationView(ctx context.Context, record conversations.Conversation, names map[string]string) (api.Conversation, error) {
	view := api.Conversation{Conversation: record, WorkerName: names[record.Worker]}
	err := h.Store.Read(ctx, h.Scope, func(tx ports.ReadTx) error {
		cards, err := control.ConversationCards(tx.Members(), tx.Approvals(), h.Scope, record)
		for _, card := range cards {
			if card.State == authority.ApprovalPending {
				view.OpenCards++
			}
		}
		return err
	})
	return view, err
}

func (h *handler) conversationViews(ctx context.Context, rows []conversations.Conversation) ([]api.Conversation, error) {
	names, err := h.workerNames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.Conversation, 0, len(rows))
	for _, row := range rows {
		view, err := h.conversationView(ctx, row, names)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

func assistantWorkers(workers []control.AssistantWorker) []api.AssistantWorker {
	out := make([]api.AssistantWorker, 0, len(workers))
	for _, worker := range workers {
		out = append(out, api.AssistantWorker{ID: worker.ID, Name: worker.Name, Available: worker.Available, Reason: worker.Reason})
	}
	return out
}

func (h *handler) listConversations(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	rows, err := h.conversations.List(r.Context(), h.Scope, actor)
	if err != nil {
		fail(w, err)
		return
	}
	views, err := h.conversationViews(r.Context(), rows)
	if err != nil {
		fail(w, err)
		return
	}
	workers, err := h.conversations.Workers(r.Context(), h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	list := api.ConversationList{Conversations: views, Workers: assistantWorkers(workers)}
	if list.AsOf, err = h.now(r.Context()); err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, list)
}

// now reads database time.
func (h *handler) now(ctx context.Context) (int64, error) {
	var now int64
	err := h.Store.Read(ctx, h.Scope, func(tx ports.ReadTx) error {
		var err error
		now, err = tx.Now()
		return err
	})
	return now, err
}

func (h *handler) answerOpened(w http.ResponseWriter, r *http.Request, opened control.Opened, status int) {
	views, err := h.conversationViews(r.Context(), append([]conversations.Conversation{opened.Conversation}, opened.Stopped...))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, status, api.ConversationOpened{Conversation: views[0], Stopped: views[1:]})
}

func (h *handler) answerConversation(w http.ResponseWriter, r *http.Request, record conversations.Conversation, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	views, err := h.conversationViews(r.Context(), []conversations.Conversation{record})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, views[0])
}

func (h *handler) createConversation(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.CreateConversation
	if !decode(w, r, &in) {
		return
	}
	opened, err := h.conversations.Create(r.Context(), h.Scope, control.NewConversation{ID: in.ID, Name: in.Name, Worker: in.Worker}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	h.answerOpened(w, r, opened, http.StatusCreated)
}

func (h *handler) showConversation(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	record, err := h.conversations.Get(r.Context(), h.Scope, r.PathValue("id"), actor)
	h.answerConversation(w, r, record, err)
}

func (h *handler) openConversation(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.OpenConversation
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	opened, err := h.conversations.Open(r.Context(), h.Scope, r.PathValue("id"), in.Worker, actor)
	if err != nil {
		fail(w, err)
		return
	}
	h.answerOpened(w, r, opened, http.StatusOK)
}

func (h *handler) stopConversation(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	record, err := h.conversations.Stop(r.Context(), h.Scope, r.PathValue("id"), actor)
	h.answerConversation(w, r, record, err)
}

func (h *handler) renameConversation(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.RenameConversation
	if !decode(w, r, &in) {
		return
	}
	record, err := h.conversations.Rename(r.Context(), h.Scope, r.PathValue("id"), in.Name, actor)
	h.answerConversation(w, r, record, err)
}

func (h *handler) deleteConversation(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	if err := h.conversations.Delete(r.Context(), h.Scope, r.PathValue("id"), actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) compactConversation(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	record, err := h.conversations.Compact(r.Context(), h.Scope, r.PathValue("id"), actor)
	h.answerConversation(w, r, record, err)
}

func (h *handler) clearConversation(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	record, err := h.conversations.Clear(r.Context(), h.Scope, r.PathValue("id"), actor)
	h.answerConversation(w, r, record, err)
}

func (h *handler) messageAssistant(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.ConversationMessage
	if !decode(w, r, &in) {
		return
	}
	sent, err := h.conversations.Send(r.Context(), h.Scope, r.PathValue("id"), control.MemberMessage{ID: in.ID, Body: in.Body, Worker: in.Worker}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	views, err := h.conversationViews(r.Context(), append([]conversations.Conversation{sent.Conversation}, sent.Stopped...))
	if err != nil {
		fail(w, err)
		return
	}
	var entry api.TalkEntry
	err = h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		entries, err := h.talkEntries(tx, []conversations.Entry{sent.Entry})
		if len(entries) == 1 {
			entry = entries[0]
		}
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, api.ConversationSent{Conversation: views[0], Entry: entry, Message: sent.Message, Stopped: views[1:]})
}

// talkLimit reads a page's limit: 100 when a request names none, and at most 500.
func talkLimit(r *http.Request) (int, error) {
	text := r.URL.Query().Get("limit")
	if text == "" {
		return api.TalkDefaultLimit, nil
	}
	limit, err := strconv.Atoi(text)
	if err != nil || limit < 1 || limit > api.TalkMaxLimit {
		return 0, fmt.Errorf("%w: limit is a number from 1 to %d, and not %q", authority.ErrInvalid, api.TalkMaxLimit, text)
	}
	return limit, nil
}

func (h *handler) conversationEntries(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	limit, err := talkLimit(r)
	if err != nil {
		fail(w, err)
		return
	}
	var before int64
	if text := r.URL.Query().Get("before"); text != "" {
		if before, err = strconv.ParseInt(text, 10, 64); err != nil || before < 1 {
			invalid(w, "before is what a page's next said, and not %q", text)
			return
		}
	}
	page, err := h.conversations.Entries(r.Context(), h.Scope, r.PathValue("id"), before, limit, actor)
	if err != nil {
		fail(w, err)
		return
	}
	out := api.TalkPage{AsOf: page.AsOf}
	if page.Next != 0 {
		out.Next = strconv.FormatInt(page.Next, 10)
	}
	err = h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		var err error
		out.Entries, err = h.talkEntries(tx, page.Entries)
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, out)
}

// talkEntries renders a conversation's entries, with a card in place of each action that became one, as
// the card stands now.
func (h *handler) talkEntries(tx ports.ReadTx, rows []conversations.Entry) ([]api.TalkEntry, error) {
	out := make([]api.TalkEntry, 0, len(rows))
	for _, row := range rows {
		entry := api.TalkEntry{ID: row.ID, At: row.RecordedAt, Kind: string(row.Kind), Body: row.Body, BeforeClear: row.BeforeClear}
		switch row.Kind {
		case conversations.KindMember:
			data, err := row.Member()
			if err != nil {
				return nil, err
			}
			entry.Assent = data.Assent
			if message, err := tx.Messages().Message(data.Message); err == nil {
				entry.Message = &message
				entry.From = &api.TalkFrom{Kind: message.From.Kind, ID: message.From.ID, Label: memberLabel(tx.Members(), message.From.ID)}
			} else if !errors.Is(err, ports.ErrNotFound) {
				return nil, err
			}
		case conversations.KindReply:
			entry.From = &api.TalkFrom{Kind: api.TalkFromAssistant, ID: row.Conversation, Label: "Assistant"}
		case conversations.KindAction:
			action, err := row.Action()
			if err != nil {
				return nil, err
			}
			entry.Action = &action
			if action.Outcome == conversations.OutcomeCard {
				entry.Kind = api.TalkKindCard
				card, err := tx.Approvals().Approval(action.Approval)
				if err != nil && !errors.Is(err, ports.ErrNotFound) {
					return nil, err
				}
				if err == nil {
					entry.Approval = &card
				}
			}
		case conversations.KindMarker:
			marker, err := row.Marker()
			if err != nil {
				return nil, err
			}
			entry.Marker = marker
		}
		out = append(out, entry)
	}
	return out, nil
}

// senderLabel names who sent a message: a member by their name, and a delegate, the assistant among them, by
// its credential's label, as the audit names it.
func senderLabel(members ports.MemberReader, kind, id string) string {
	_, credential, ok := authority.ActingFor(ledger.Actor{Kind: ledger.ActorKind(kind), ID: id})
	if !ok {
		return memberLabel(members, id)
	}
	if record, err := members.Delegate(credential); err == nil && record.Label != "" {
		return record.Label
	}
	return credential
}

func memberLabel(members ports.MemberReader, id string) string {
	if record, err := members.Member(id); err == nil && record.DisplayName != "" {
		return record.DisplayName
	}
	return id
}

// pageOf takes one page of a stream sorted oldest first: the newest entries before a cursor, oldest first
// within the page. A cursor is an entry's time and ID.
func pageOf(entries []api.TalkEntry, before string, limit int) ([]api.TalkEntry, string, error) {
	slices.SortStableFunc(entries, func(a, b api.TalkEntry) int { return cmp.Or(cmp.Compare(a.At, b.At), strings.Compare(a.ID, b.ID)) })
	end := len(entries)
	if before != "" {
		at, id, found := strings.Cut(before, ".")
		ms, err := strconv.ParseInt(at, 10, 64)
		if !found || err != nil {
			return nil, "", fmt.Errorf("%w: before is what a page's next said, and not %q", authority.ErrInvalid, before)
		}
		end = 0
		for i, entry := range entries {
			if entry.At < ms || (entry.At == ms && entry.ID < id) {
				end = i + 1
			}
		}
	}
	start := max(0, end-limit)
	page := slices.Clone(entries[start:end])
	next := ""
	if start > 0 {
		next = strconv.FormatInt(page[0].At, 10) + "." + page[0].ID
	}
	if page == nil {
		page = []api.TalkEntry{}
	}
	return page, next, nil
}

// messageEntries renders the messages to a target and each reply they had, at the times they were sent
// and answered.
func messageEntries(tx ports.ReadTx, rows []messages.Message, from api.TalkFrom) ([]api.TalkEntry, error) {
	var out []api.TalkEntry
	for _, row := range rows {
		message := row
		out = append(out, api.TalkEntry{
			ID: "message-" + row.ID, At: row.CreatedAt, Kind: api.TalkKindMember, Body: row.Body, Message: &message,
			From: &api.TalkFrom{Kind: row.From.Kind, ID: row.From.ID, Label: senderLabel(tx.Members(), row.From.Kind, row.From.ID)},
		})
		if row.Reply == "" {
			continue
		}
		events, err := tx.EventsAbout(ledger.Subject{Kind: messages.SubjectMessage, ID: row.ID})
		if err != nil {
			return nil, err
		}
		at := row.CreatedAt
		for _, event := range events {
			if strings.Contains(string(event.Data), `"state":"replied"`) {
				at = event.RecordedAt
				break
			}
		}
		replyFrom := from
		out = append(out, api.TalkEntry{ID: "reply-" + row.ID, At: at, Kind: api.TalkKindReply, Body: row.Reply, From: &replyFrom, Message: &message})
	}
	return out, nil
}

// eventEntries renders events as lines of a stream, naming each delegate by its credential's label.
func eventEntries(tx ports.ReadTx, events []ledger.Event) ([]api.TalkEntry, error) {
	stream := make([]api.StreamEvent, 0, len(events))
	for _, event := range events {
		stream = append(stream, api.StreamEventOf(event))
	}
	if err := labelsOf(tx.Members(), stream); err != nil {
		return nil, err
	}
	out := make([]api.TalkEntry, 0, len(stream))
	for i := range stream {
		event := stream[i]
		out = append(out, api.TalkEntry{ID: "event-" + strconv.FormatInt(event.Rev, 10), At: event.RecordedAt, Kind: api.TalkKindEvent, Event: &event})
	}
	return out, nil
}

// requestEntries renders approval requests as they stand now, at the time each was raised.
func requestEntries(rows []authority.Approval) []api.TalkEntry {
	out := make([]api.TalkEntry, 0, len(rows))
	for _, row := range rows {
		approval := row
		out = append(out, api.TalkEntry{ID: "approval-" + row.ID, At: row.RaisedAt, Kind: api.TalkKindRequest, Approval: &approval})
	}
	return out
}

// questionEntry renders a question as it stands now, at the time it was recorded, with where an answer came
// from when a member's answer recorded it.
func questionEntry(tx ports.ReadTx, question questions.Question) (api.TalkEntry, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: questions.SubjectQuestion, ID: question.ID})
	if err != nil {
		return api.TalkEntry{}, err
	}
	at := int64(0)
	if len(events) != 0 {
		at = events[0].RecordedAt
	}
	held := api.TalkQuestion{Question: question, Batch: question.BatchID}
	if question.DecisionID != "" {
		decided, err := tx.EventsAbout(ledger.Subject{Kind: questions.SubjectDecision, ID: question.DecisionID})
		if err != nil {
			return api.TalkEntry{}, err
		}
		held.Channel = control.DecisionChannel(decided)
	}
	return api.TalkEntry{ID: "question-" + question.ID, At: at, Kind: api.TalkKindQuestion, Question: &held}, nil
}

// streamPage answers one page of a stream.
func (h *handler) streamPage(w http.ResponseWriter, r *http.Request, read func(tx ports.ReadTx) ([]api.TalkEntry, error)) {
	limit, err := talkLimit(r)
	if err != nil {
		fail(w, err)
		return
	}
	var page api.TalkPage
	err = h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		entries, err := read(tx)
		if err != nil {
			return err
		}
		if page.Entries, page.Next, err = pageOf(entries, r.URL.Query().Get("before"), limit); err != nil {
			return err
		}
		page.AsOf, err = tx.Now()
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, page)
}

// laneStream is a lane's stream, for a headless lane or a planning session: the messages members and their
// delegates sent it, with their delivery state, its replies, the events about it or its work order, its approval requests,
// and the questions it asked or is attached to.
func (h *handler) laneStream(w http.ResponseWriter, r *http.Request, _ ledger.Actor) {
	lane := r.PathValue("id")
	h.streamPage(w, r, func(tx ports.ReadTx) ([]api.TalkEntry, error) {
		row, err := tx.Lanes().Row(lane)
		if err != nil {
			return nil, err
		}
		sent, err := tx.Messages().ToLane(lane)
		if err != nil {
			return nil, err
		}
		sent = slices.DeleteFunc(sent, func(m messages.Message) bool {
			return m.From.Kind != string(ledger.ActorMember) && m.From.Kind != string(ledger.ActorDelegate)
		})
		entries, err := messageEntries(tx, sent, api.TalkFrom{Kind: string(ledger.ActorLane), ID: lane, Label: "Lane " + lane})
		if err != nil {
			return nil, err
		}
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectLane, ID: lane})
		if err != nil {
			return nil, err
		}
		about, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: row.WorkOrder})
		if err != nil {
			return nil, err
		}
		lines, err := eventEntries(tx, append(events, about...))
		if err != nil {
			return nil, err
		}
		entries = append(entries, lines...)
		requests, err := tx.Approvals().OfLane(lane)
		if err != nil {
			return nil, err
		}
		entries = append(entries, requestEntries(requests)...)
		asked, err := laneQuestions(tx, lane)
		if err != nil {
			return nil, err
		}
		for _, question := range asked {
			entry, err := questionEntry(tx, question)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		}
		return entries, nil
	})
}

// laneQuestions lists the questions a lane asked, or is attached to.
func laneQuestions(tx ports.ReadTx, lane string) ([]questions.Question, error) {
	attachments, err := tx.Questions().Attachments("")
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, attachment := range attachments {
		if attachment.Lane == lane {
			ids[attachment.QuestionID] = true
		}
	}
	all, err := tx.Questions().Questions(ports.RecordFilter{})
	if err != nil {
		return nil, err
	}
	var out []questions.Question
	for _, question := range all {
		if ids[question.ID] || question.AskedBy == lane {
			out = append(out, question)
		}
	}
	return out, nil
}

// orchestratorStream is a worker's orchestrator's stream in this project: the events its orchestrator
// caused here, the member's messages to it with its replies, and the recovery requests it raised.
func (h *handler) orchestratorStream(w http.ResponseWriter, r *http.Request, _ ledger.Actor) {
	worker := r.PathValue("worker")
	if _, err := h.plane.Orchestrator(r.Context(), worker); err != nil && !errors.Is(err, ports.ErrNotFound) {
		fail(w, err)
		return
	}
	names, err := h.workerNames(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	if _, ok := names[worker]; !ok {
		fail(w, fmt.Errorf("worker %s: %w", worker, ports.ErrNotFound))
		return
	}
	self := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: worker}
	h.streamPage(w, r, func(tx ports.ReadTx) ([]api.TalkEntry, error) {
		sent, err := tx.Messages().ToTarget(messages.TargetOrchestrator, worker)
		if err != nil {
			return nil, err
		}
		entries, err := messageEntries(tx, sent, api.TalkFrom{Kind: string(ledger.ActorOrchestrator), ID: worker, Label: "The orchestrator on " + names[worker]})
		if err != nil {
			return nil, err
		}
		var caused []ledger.Event
		for after := int64(0); ; {
			page, err := tx.Events(after, 1000)
			if err != nil {
				return nil, err
			}
			if len(page) == 0 {
				break
			}
			for _, event := range page {
				if event.Actor == self {
					caused = append(caused, event)
				}
			}
			after = page[len(page)-1].Rev
		}
		lines, err := eventEntries(tx, caused)
		if err != nil {
			return nil, err
		}
		entries = append(entries, lines...)
		all, err := tx.Approvals().All()
		if err != nil {
			return nil, err
		}
		raised := slices.DeleteFunc(all, func(a authority.Approval) bool {
			return a.Kind != authority.KindRecovery || a.RequestedBy != authority.Principal(self)
		})
		return append(entries, requestEntries(raised)...), nil
	})
}

// conversationTargets is what the calling member can talk to now.
func (h *handler) conversationTargets(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	ctx := r.Context()
	var targets api.ConversationTargets
	rows, err := h.conversations.List(ctx, h.Scope, actor)
	if err != nil {
		fail(w, err)
		return
	}
	if len(rows) != 0 {
		views, err := h.conversationViews(ctx, rows[:1])
		if err != nil {
			fail(w, err)
			return
		}
		targets.Assistant.Conversation = &views[0]
	}
	workers, err := h.conversations.Workers(ctx, h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	targets.Assistant.Workers = assistantWorkers(workers)
	if targets.Orchestrators, err = h.orchestratorTargets(ctx); err != nil {
		fail(w, err)
		return
	}
	names, err := h.workerNames(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	err = h.Store.Read(ctx, h.Scope, func(tx ports.ReadTx) error {
		policy, err := control.AssistantPolicyOf(tx.Policies())
		if err != nil {
			return err
		}
		targets.Assistant.IdleStopMs = policy.IdleStopAfter().Milliseconds()
		if targets.Planning, targets.Lanes, err = sessionTargets(tx, names); err != nil {
			return err
		}
		targets.AsOf, err = tx.Now()
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, targets)
}

// orchestratorTargets lists the orchestrator of each worker that holds a checkout of an active repository
// of this project, with the projects it serves.
func (h *handler) orchestratorTargets(ctx context.Context) ([]api.OrchestratorTarget, error) {
	out := []api.OrchestratorTarget{}
	if h.AssistantHosts == nil {
		return out, nil
	}
	hosts, err := h.AssistantHosts(ctx, h.Scope)
	if err != nil {
		return nil, err
	}
	var repositories []ledger.Repository
	err = h.Store.Read(ctx, h.Scope, func(tx ports.ReadTx) error {
		var err error
		repositories, err = tx.Repositories().Repositories()
		return err
	})
	if err != nil {
		return nil, err
	}
	projects, err := h.Projects.List(ctx)
	if err != nil {
		return nil, err
	}
	workers, err := h.plane.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, worker := range workers {
		host := hosts[worker.ID]
		if !slices.ContainsFunc(repositories, func(repository ledger.Repository) bool { return !repository.Removed() && host.Checkouts[repository.ID] }) {
			continue
		}
		target := api.OrchestratorTarget{Worker: worker.ID, Name: worker.Name, State: ledger.OrchestratorStopped, Desired: ledger.OrchestratorStopped, Projects: []string{}}
		row, err := h.plane.Orchestrator(ctx, worker.ID)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return nil, err
		}
		if err == nil {
			target.Desired, target.Generation = row.Desired, row.Generation
			target.State = cmp.Or(row.Actual, row.Desired)
			if row.Desired != ledger.OrchestratorRunning {
				target.State = ledger.OrchestratorStopped
			}
		}
		for _, project := range projects {
			if project.State != ledger.ProjectActive {
				continue
			}
			served, err := h.AssistantHosts(ctx, project.ID)
			if err != nil {
				return nil, err
			}
			if len(served[worker.ID].Checkouts) != 0 {
				target.Projects = append(target.Projects, project.Name)
			}
		}
		out = append(out, target)
	}
	return out, nil
}

// sessionTargets lists the lanes a member can talk to: the planning sessions, and every other lane, of
// work that has not ended.
func sessionTargets(tx ports.ReadTx, names map[string]string) ([]api.SessionTarget, []api.SessionTarget, error) {
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return nil, nil, err
	}
	planning, lanes := []api.SessionTarget{}, []api.SessionTarget{}
	for _, row := range rows {
		if row.Status.ActualState == ledger.StateEnded {
			continue
		}
		order, err := tx.WorkOrders().Get(row.WorkOrder)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		target := api.SessionTarget{
			Lane: row.ID, WorkOrder: order.ID, Title: order.Title, Kind: string(order.Kind), Worker: row.WorkerID, WorkerName: names[row.WorkerID],
			Generation: row.Generation, State: cmp.Or(row.Status.ActualState, "pending"),
		}
		if order.Adopt {
			target.Kind = "adopt"
		}
		if experience, err := tx.Measures().Experience("lane", row.ID); err == nil {
			target.RunMode = experience.Settings.RunMode
		}
		if order.Kind == workorders.KindPlan {
			planning = append(planning, target)
		} else {
			lanes = append(lanes, target)
		}
	}
	return planning, lanes, nil
}
