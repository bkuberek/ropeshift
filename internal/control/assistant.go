package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// opAssistant names the transactions a worker makes for an assistant's run.
const opAssistant = "assistant.report"

// runBoundary reads the marker where a conversation's current run began: its run lane, the conversation's
// generation at the run's first one, whether it began after a clear, and how many compactions the member
// asked of the run since.
func runBoundary(reader ports.ConversationReader, record conversations.Conversation) (conversations.MarkerData, int64, error) {
	var compactions int64
	before := int64(0)
	for {
		rows, err := reader.Entries(record.ID, before, 200)
		if err != nil {
			return conversations.MarkerData{}, 0, err
		}
		if len(rows) == 0 {
			return conversations.MarkerData{RunLane: record.RunLane, Generation: record.Generation}, compactions, nil
		}
		for _, row := range rows {
			if row.Kind != conversations.KindMarker {
				continue
			}
			data, err := row.MarkerData()
			if err != nil {
				return conversations.MarkerData{}, 0, err
			}
			if data.Marker == conversations.MarkerCompacted {
				compactions++
			}
			if data.Boundary() && data.RunLane == record.RunLane {
				return data, compactions, nil
			}
		}
		before = rows[len(rows)-1].Seq
	}
}

// runGeneration is the run's own generation of the conversation's current one: a run's first generation is
// the one its boundary names.
func runGeneration(boundary conversations.MarkerData, conversationGeneration int64) int64 {
	return conversationGeneration - boundary.Generation + 1
}

// desiredAssistants lists the running conversations placed on a worker, with their messages no worker has
// acknowledged. The assistant policy is the installation's, read here through the project's view of it.
func desiredAssistants(tx ports.ReadTx, worker string) ([]ledger.DesiredAssistant, error) {
	running, err := tx.Conversations().Running()
	if err != nil || len(running) == 0 {
		return nil, err
	}
	policy, err := assistantInForce(tx.Policies())
	if err != nil {
		return nil, err
	}
	now, err := tx.Now()
	if err != nil {
		return nil, err
	}
	var out []ledger.DesiredAssistant
	for _, row := range running {
		if row.Worker != worker || row.RunLane == "" {
			continue
		}
		boundary, compactions, err := runBoundary(tx.Conversations(), row)
		if err != nil {
			return nil, err
		}
		desired := ledger.DesiredAssistant{
			Conversation: row.ID, Member: row.Member, Lane: row.RunLane, Generation: boundary.Generation,
			IdleStopMs: policy.IdleStopAfter().Milliseconds(), Fresh: boundary.Marker == conversations.MarkerCleared, Compactions: compactions,
		}
		if member, err := tx.Members().Member(row.Member); err == nil {
			desired.MemberName = member.DisplayName
		}
		queued, err := tx.Messages().QueuedTo(messages.TargetAssistant, row.ID)
		if err != nil {
			return nil, err
		}
		for _, m := range queued {
			desired.Messages = append(desired.Messages, ledger.DesiredMessage{
				ID: m.ID, Lane: row.RunLane, Generation: runGeneration(boundary, m.To.Generation), FromKind: m.From.Kind, ReplyTo: m.ReplyTo,
				Kind: string(m.Kind), Body: m.Body, Traceparent: m.Traceparent, AgeMs: now - m.CreatedAt,
				StaleAfterMs: m.StaleAt - m.CreatedAt, ExpireAfterMs: m.ExpiresAt - m.CreatedAt,
			})
		}
		out = append(out, desired)
	}
	return out, nil
}

// placedRun reads the conversation a worker's run serves, and refuses a run that is not this conversation's
// current one on this worker.
func placedRun(reader ports.ConversationReader, worker, conversation, lane string) (conversations.Conversation, error) {
	record, err := reader.Conversation(conversation)
	if err != nil {
		return conversations.Conversation{}, err
	}
	if record.Worker != worker || record.RunLane != lane || !record.Running() {
		return conversations.Conversation{}, fmt.Errorf("%w: run %s is not conversation %s's on this worker", workorders.ErrInvalid, lane, conversation)
	}
	return record, nil
}

// advance moves a conversation to the generation its run began, which only rises.
func advance(tx ports.WriteTx, record conversations.Conversation, generation int64, actor ledger.Actor, stamp func(*ledger.EventDraft)) (conversations.Conversation, error) {
	if generation <= record.Generation {
		return record, nil
	}
	record.Generation = generation
	if err := tx.Conversations().Save(record); err != nil {
		return conversations.Conversation{}, err
	}
	event, err := conversations.ChangedEvent(record, actor)
	if err != nil {
		return conversations.Conversation{}, err
	}
	stamp(&event)
	_, err = tx.Append(event)
	return record, err
}

// AssistantCredential makes the credential of one generation of an assistant's run: a delegate credential of
// kind assistant, for the conversation's member in this project, with no expiry. It acts only while that
// generation runs, and the token is returned this once. The grant also carries what the session's first
// prompt reads.
func (w *Workers) AssistantCredential(ctx context.Context, request ledger.AssistantCredentialRequest) (ledger.AssistantGrant, error) {
	worker := ledger.Actor{Kind: ledger.ActorWorker, ID: request.Auth.WorkerID}
	if request.Generation < 1 {
		return ledger.AssistantGrant{}, fmt.Errorf("%w: a generation counts from 1", workorders.ErrInvalid)
	}
	var record conversations.Conversation
	err := w.store.Write(ctx, w.scope, opAssistant, func(tx ports.WriteTx) error {
		if err := w.checkSession(tx, request.Auth, request.Epoch); err != nil {
			return err
		}
		var err error
		if record, err = placedRun(tx.Conversations(), worker.ID, request.Conversation, request.Lane); err != nil {
			return err
		}
		boundary, _, err := runBoundary(tx.Conversations(), record)
		if err != nil {
			return err
		}
		generation := boundary.Generation + request.Generation - 1
		if generation < record.Generation {
			return fmt.Errorf("%w: generation %d of run %s ended", workorders.ErrInvalid, request.Generation, request.Lane)
		}
		if generation == record.Generation {
			return errNothingToStop
		}
		record, err = advance(tx, record, generation, worker, noStamp)
		return err
	})
	if err != nil && !errors.Is(err, errNothingToStop) {
		return ledger.AssistantGrant{}, err
	}
	// Every credential of the conversation ends before the new one exists: an earlier generation's, and
	// this generation's own from an earlier call, which a retry leaves unused.
	if err := revokeAssistant(ctx, w.store.Installation(w.scope), w.scope, record, record.Generation+1, authority.DelegateEndGeneration, worker); err != nil {
		return ledger.AssistantGrant{}, err
	}
	token, err := newSecret()
	if err != nil {
		return ledger.AssistantGrant{}, err
	}
	grant := ledger.AssistantGrant{Token: token}
	err = w.store.Installation(w.scope).Write(ctx, "delegate.assistant", func(tx ports.SharedTx) error {
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		credential := authority.DelegateCredential{
			ID: authority.DelegateIDPrefix + id, Digest: digestOf(token), Member: record.Member, Project: w.scope, Label: authority.AssistantLabel,
			Kind: authority.DelegateAssistant, CreatedAt: tx.Now(), Conversation: record.ID, Generation: record.Generation,
		}
		if err := tx.Members().InsertDelegate(credential); err != nil {
			return err
		}
		grant.Credential = credential.ID
		return appendDelegate(tx, EventDelegateCreated, credential, worker)
	})
	if err != nil {
		return ledger.AssistantGrant{}, err
	}
	err = w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		var err error
		if grant.Context, err = assistantContext(tx, record); err != nil {
			return err
		}
		grant.History, err = assistantHistory(tx.Conversations(), record.ID)
		return err
	})
	return grant, err
}

// assistantContext is what an assistant's pack states of its project: its name, its active repositories,
// its open plans, and how much work stands in each state.
func assistantContext(tx ports.ReadTx, record conversations.Conversation) (ledger.AssistantContext, error) {
	workspace, err := tx.Workspace()
	if err != nil {
		return ledger.AssistantContext{}, err
	}
	out := ledger.AssistantContext{Project: string(workspace.ID), ProjectName: workspace.Name, Member: record.Member, Work: map[string]int64{}}
	if member, err := tx.Members().Member(record.Member); err == nil {
		out.MemberName = member.DisplayName
	}
	repositories, err := tx.Repositories().Repositories()
	if err != nil {
		return ledger.AssistantContext{}, err
	}
	for _, repository := range repositories {
		if !repository.Removed() {
			out.Repositories = append(out.Repositories, ledger.AssistantRepository{ID: repository.ID, Name: repository.Name, Base: repository.Base()})
		}
	}
	slices.SortFunc(out.Repositories, func(a, b ledger.AssistantRepository) int { return strings.Compare(a.Name, b.Name) })
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return ledger.AssistantContext{}, err
	}
	for _, order := range orders {
		out.Work[string(order.State)]++
		if order.Kind == workorders.KindPlan && !workorders.Terminal(order.State) {
			out.Plans = append(out.Plans, ledger.AssistantPlan{ID: order.ID, Title: order.Title})
		}
	}
	return out, nil
}

// assistantHistory is a conversation's newest entries since its last clear, oldest first, as a fresh
// generation's first prompt states them. A clear starts the next generation with an empty context, so
// nothing from before it reaches a session again (ADR-0024).
func assistantHistory(reader ports.ConversationReader, conversation string) ([]ledger.AssistantHistoryEntry, error) {
	rows, err := reader.Entries(conversation, 0, conversations.HistoryEntries)
	if err != nil {
		return nil, err
	}
	if cleared := slices.IndexFunc(rows, func(row conversations.Entry) bool { return row.BeforeClear }); cleared >= 0 {
		rows = rows[:cleared]
	}
	slices.Reverse(rows)
	out := make([]ledger.AssistantHistoryEntry, 0, len(rows))
	for _, row := range rows {
		entry := ledger.AssistantHistoryEntry{Kind: string(row.Kind), Body: row.Body, At: row.RecordedAt}
		switch row.Kind {
		case conversations.KindAction:
			if action, err := row.Action(); err == nil {
				entry.Body = action.Label + " (" + action.Outcome + ")"
			}
		case conversations.KindMarker:
			if marker, err := row.Marker(); err == nil {
				entry.Body = marker
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// AssistantEntry records a reply an assistant's run wrote, once: the entry's ID derives from the vendor's
// message, so a retry finds the first. A reply counts as activity.
func (w *Workers) AssistantEntry(ctx context.Context, request ledger.AssistantEntryRequest) error {
	worker := ledger.Actor{Kind: ledger.ActorWorker, ID: request.Auth.WorkerID}
	if request.ID == "" {
		return fmt.Errorf("%w: a reply names the vendor's message", workorders.ErrInvalid)
	}
	err := w.store.Write(ctx, w.scope, opAssistant, func(tx ports.WriteTx) error {
		if err := w.checkSession(tx, request.Auth, request.Epoch); err != nil {
			return err
		}
		record, err := placedRun(tx.Conversations(), worker.ID, request.Conversation, request.Lane)
		if err != nil {
			return err
		}
		id := messages.DerivedID("reply", record.ID, request.ID)
		if _, err := tx.Conversations().Entry(record.ID, id); err == nil {
			return errNothingToStop
		} else if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		entry, err := conversations.NewEntry(record.ID, id, conversations.KindReply, request.Body, conversations.ReplyData{Source: request.ID})
		if err != nil {
			return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
		}
		if _, err := addEntry(tx, entry, worker); err != nil {
			return err
		}
		record.LastActiveAt = tx.Now()
		return saveConversation(tx, record, worker)
	})
	if errors.Is(err, errNothingToStop) {
		return nil
	}
	return err
}

// assistantStates are the conversation states a run's lane state reads as.
var assistantStates = map[lanes.ActualState]conversations.ActualState{
	lanes.Working: conversations.Working, lanes.Compacting: conversations.Working, lanes.Briefing: conversations.Working,
	lanes.Idle: conversations.Running, lanes.NeedsDecision: conversations.Running,
}

// assistantUpload stores what a worker says of an assistant's run, and says whether the lane is one. A
// run's start, its states, a new generation, and its end move the conversation; anything else is the
// worker's note. Every one is stored under its upload's ID, so a retry stores nothing twice.
func assistantUpload(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) (bool, error) {
	record, err := tx.Conversations().ByRunLane(e.Lane)
	if errors.Is(err, ports.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if record.Worker != worker {
		return true, fmt.Errorf("%w: run %s is not this worker's", workorders.ErrInvalid, e.Lane)
	}
	actor := ledger.Actor{Kind: ledger.ActorWorker, ID: worker}
	stamp := func(draft *ledger.EventDraft) { draft.ID, draft.OccurredAt = id, e.OccurredAt }
	moved := record
	switch e.Type {
	case ledger.UploadMessage:
		return true, messageUpload(tx, worker, id, e)
	case ledger.UploadLaneStatus:
		var status ledger.LaneStatus
		if err := json.Unmarshal(e.Payload, &status); err != nil {
			return true, fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
		}
		if actual, ok := assistantStates[lanes.ActualState(status.ActualState)]; ok && record.Running() {
			moved.Actual = actual
		}
	case ledger.UploadGenerationStarted:
		boundary, _, err := runBoundary(tx.Conversations(), record)
		if err != nil {
			return true, err
		}
		if generation := boundary.Generation + e.Generation - 1; generation > record.Generation {
			moved.Generation = generation
		}
	case ledger.UploadLaneEnded:
		switch {
		case record.Running() && record.Actual == conversations.Starting:
			moved.Desired, moved.Actual, moved.StoppedReason = conversations.DesiredStopped, conversations.Failed, conversations.ReasonFailed
		case record.Running():
			moved.Desired, moved.Actual, moved.StoppedReason = conversations.DesiredStopped, conversations.Stopped, conversations.ReasonSessionEnd
		default:
			moved.Actual = conversations.Stopped
		}
	}
	if moved == record {
		draft, err := uploadedDraft(tx, worker, id, ledger.UploadEvent{Epoch: e.Epoch, Seq: e.Seq, Type: e.Type, OccurredAt: e.OccurredAt})
		if err != nil {
			return true, err
		}
		_, err = tx.Append(draft)
		return true, err
	}
	if err := tx.Conversations().Save(moved); err != nil {
		return true, err
	}
	event, err := conversations.ChangedEvent(moved, actor)
	if err != nil {
		return true, err
	}
	stamp(&event)
	if _, err := tx.Append(event); err != nil {
		return true, err
	}
	if record.Running() && !moved.Running() {
		_, err = addMarker(tx, moved, conversations.MarkerData{Marker: conversations.MarkerStopped}, actor)
	}
	return true, err
}

// afterAssistantUpload ends the credentials a run's upload left behind: those of a generation that ended,
// and every one of a run that ended. It runs once the upload committed, in the installation.
func (w *Workers) afterAssistantUpload(ctx context.Context, worker string, e ledger.UploadEvent) {
	if e.WorkOrder != "" || (e.Type != ledger.UploadLaneEnded && e.Type != ledger.UploadGenerationStarted) {
		return
	}
	var record conversations.Conversation
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		var err error
		record, err = tx.Conversations().ByRunLane(e.Lane)
		return err
	})
	if err != nil {
		return
	}
	below, reason := record.Generation, authority.DelegateEndGeneration
	if e.Type == ledger.UploadLaneEnded {
		below, reason = 0, authority.DelegateEndStopped
	}
	_ = revokeAssistant(ctx, w.store.Installation(w.scope), w.scope, record, below, reason, ledger.Actor{Kind: ledger.ActorWorker, ID: worker})
}

// AssistantActs refuses an assistant credential unless its conversation's session runs the credential's
// generation now. Revocation ends a credential too; this holds even where a revocation was lost.
func AssistantActs(ctx context.Context, store ports.Store, scope ledger.WorkspaceID, credential authority.DelegateCredential) error {
	if !credential.Assistant() {
		return nil
	}
	return store.Read(ctx, scope, func(tx ports.ReadTx) error {
		record, err := tx.Conversations().Conversation(credential.Conversation)
		if errors.Is(err, ports.ErrNotFound) {
			return fmt.Errorf("%w: delegate credential %s names no conversation", authority.ErrBadDelegate, credential.ID)
		}
		if err != nil {
			return err
		}
		if record.Member != credential.Member || record.Generation != credential.Generation || !record.Live() {
			return fmt.Errorf("%w: delegate credential %s's session is not running", authority.ErrBadDelegate, credential.ID)
		}
		return nil
	})
}

// ConversationOfCredential names the conversation an assistant credential acts in, and whether the
// conversation still exists. An external credential names none.
func ConversationOfCredential(ctx context.Context, store ports.Store, scope ledger.WorkspaceID, credentialID string) (conversations.Conversation, bool, error) {
	var credential authority.DelegateCredential
	err := store.Installation(scope).Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		credential, err = tx.Members().Delegate(credentialID)
		return err
	})
	if err != nil || !credential.Assistant() {
		return conversations.Conversation{}, false, err
	}
	var record conversations.Conversation
	err = store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		record, err = tx.Conversations().Conversation(credential.Conversation)
		return err
	})
	if errors.Is(err, ports.ErrNotFound) {
		return conversations.Conversation{ID: credential.Conversation}, false, nil
	}
	return record, err == nil, err
}
