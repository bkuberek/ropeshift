package worker

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
)

// orchestratorMessages is what a worker knows of the members' messages to its orchestrator, which reach it
// as items of its wake and not through a session's input: the project of each, and the word it last sent.
type orchestratorMessages struct {
	mu       sync.Mutex
	projects map[string]string
	said     map[string]messages.State
}

func (w *Worker) heardMessages() *orchestratorMessages {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.toOrchestrator == nil {
		w.toOrchestrator = &orchestratorMessages{projects: map[string]string{}, said: map[string]messages.State{}}
	}
	return w.toOrchestrator
}

// reportMessage records one word on a message to the orchestrator, once for each state. It travels in the
// message's project, as no lane's, because the orchestrator holds no lease there.
func (w *Worker) reportMessage(ctx context.Context, id string, report messages.Report) {
	heard := w.heardMessages()
	heard.mu.Lock()
	project, known := heard.projects[id]
	if !known || heard.said[id] == report.State || (report.State == messages.AtWorker && heard.said[id] != "") {
		heard.mu.Unlock()
		return
	}
	heard.said[id] = report.State
	heard.mu.Unlock()
	event, err := w.messageEvent(lanes.Record{Project: project}, report)
	if err == nil {
		_, err = w.Record(ctx, event)
	}
	if err != nil {
		w.config.Report(fmt.Errorf("message %s to the orchestrator: %w", id, err))
		return
	}
	go w.uploadSoon(ctx)
}

// noteOrchestratorMessages takes the messages a digest holds: the worker holds each one now.
func (w *Worker) noteOrchestratorMessages(ctx context.Context, items []ledger.WakeItem) {
	heard := w.heardMessages()
	for _, item := range items {
		if item.Kind != ledger.WakeMessage {
			continue
		}
		heard.mu.Lock()
		heard.projects[item.ID] = item.Project
		heard.mu.Unlock()
		w.reportMessage(ctx, item.ID, messages.Report{ID: item.ID, State: messages.AtWorker})
	}
}

// orchestratorMessagesLanded says the messages of a wake reached a turn of the orchestrator's session.
func (w *Worker) orchestratorMessagesLanded(ctx context.Context, items []ledger.WakeItem) {
	for _, item := range items {
		if item.Kind == ledger.WakeMessage {
			w.reportMessage(ctx, item.ID, messages.Report{ID: item.ID, State: messages.HandedOff})
		}
	}
}

// orchestratorReply keeps the orchestrator's answer to a member's message, through the reply path a lane
// answers by. It answers only a message its wake held.
func (w *Worker) orchestratorReply(ctx context.Context, request lanes.ReplyRequest) lanes.Reply {
	if strings.TrimSpace(request.Body) == "" {
		return refused(lanes.CodeInvalid, "a reply needs a body")
	}
	heard := w.heardMessages()
	heard.mu.Lock()
	_, known := heard.projects[request.ReplyTo]
	heard.mu.Unlock()
	if !known {
		return refused(lanes.CodeInvalid, "the orchestrator holds no message "+request.ReplyTo)
	}
	w.reportMessage(ctx, request.ReplyTo, messages.Report{ID: request.ReplyTo, State: messages.Replied, Reply: request.Body})
	return lanes.Reply{OK: true}
}
