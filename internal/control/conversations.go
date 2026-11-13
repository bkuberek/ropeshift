package control

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The operations of a conversation.
const (
	opConversationWrite = "conversation.write"
	opConversationIdle  = "conversation.idle_stop"
)

// sentence is a refusal whose message is a sentence a person reads as it stands, and which still reads as
// the error it wraps.
type sentence struct {
	base error
	text string
}

func (s sentence) Error() string { return s.text }
func (s sentence) Unwrap() error { return s.base }

func refuseWith(base error, format string, args ...any) error {
	return sentence{base: base, text: fmt.Sprintf(format, args...)}
}

// AssistantHost is what one worker could run the assistant on: whether its driver offers a headless
// session, and the repositories of a project it holds a checkout of.
type AssistantHost struct {
	Headless  bool
	Checkouts map[string]bool
}

// AssistantHosts reads what each worker could run the assistant on in a project, by worker ID. A worker
// it says nothing of holds no checkout and runs no headless session.
type AssistantHosts func(ctx context.Context, project ledger.WorkspaceID) (map[string]AssistantHost, error)

// AssistantWorker is one connected or registered worker, and whether it can run this project's assistant.
type AssistantWorker struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Opened is a conversation an act started, with the running conversations it stopped first.
type Opened struct {
	Conversation conversations.Conversation   `json:"conversation"`
	Stopped      []conversations.Conversation `json:"stopped"`
}

// Sent is a member's message to the assistant: the conversation, the entry, the message, and the running
// conversations it stopped when it opened this one.
type Sent struct {
	Conversation conversations.Conversation   `json:"conversation"`
	Entry        conversations.Entry          `json:"entry"`
	Message      messages.Message             `json:"message"`
	Stopped      []conversations.Conversation `json:"stopped"`
}

// NewConversation is what a member asks of a new conversation. ID is the client's, so a retry makes one.
type NewConversation struct {
	ID     string
	Name   string
	Worker string
}

// Conversations holds the use cases of a member's conversations with the assistant (ADR-0024). Every
// change is the member's own: a delegate reads its member's conversations and changes none.
type Conversations struct {
	store       ports.Store
	windows     messages.Windows
	hosts       AssistantHosts
	heartbeatMs int64
}

// NewConversations returns the use cases over a Store. A kind the windows leave out takes the default.
func NewConversations(store ports.Store, windows messages.Windows) *Conversations {
	return &Conversations{store: store, windows: windows, heartbeatMs: defaultHeartbeatMs}
}

// Hosting reads where the assistant can run from hosts, with workers beating at heartbeatMs. Nil hosts
// leaves no worker able to.
func (c *Conversations) Hosting(hosts AssistantHosts, heartbeatMs int64) *Conversations {
	c.hosts = hosts
	if heartbeatMs > 0 {
		c.heartbeatMs = heartbeatMs
	}
	return c
}

// memberOf is the member an actor reads conversations for: a member, or the member a delegate acts for.
func memberOf(actor ledger.Actor) string {
	member, _ := authority.MemberOf(actor)
	return member
}

// ownConversation reads a conversation of a member. Another member's reads as missing, so a member learns
// nothing of conversations that are not theirs.
func ownConversation(reader ports.ConversationReader, id, member string) (conversations.Conversation, error) {
	record, err := reader.Conversation(id)
	if err != nil {
		return record, err
	}
	if member == "" || record.Member != member {
		return conversations.Conversation{}, fmt.Errorf("conversation %s: %w", id, ports.ErrNotFound)
	}
	return record, nil
}

// changing is the check every change makes: the member's own act.
func changing(actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionConverse) {
		return fmt.Errorf("%w: %w: only a member changes their conversations", workorders.ErrForbidden, authority.ErrMemberOnly)
	}
	return nil
}

// Workers lists each registered worker with whether it can run this project's assistant: it is connected,
// holds a checkout of every active repository of the project, and its driver offers a headless session.
func (c *Conversations) Workers(ctx context.Context, scope ledger.WorkspaceID) ([]AssistantWorker, error) {
	var workers []ledger.Worker
	var repositories []ledger.Repository
	var now int64
	err := c.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		if now, err = tx.Now(); err != nil {
			return err
		}
		if workers, err = tx.Workers().List(); err != nil {
			return err
		}
		repositories, err = tx.Repositories().Repositories()
		return err
	})
	if err != nil {
		return nil, err
	}
	hosts := map[string]AssistantHost{}
	if c.hosts != nil {
		if hosts, err = c.hosts(ctx, scope); err != nil {
			return nil, err
		}
	}
	active := slices.DeleteFunc(slices.Clone(repositories), func(r ledger.Repository) bool { return r.Removed() })
	slices.SortFunc(active, func(a, b ledger.Repository) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(workers, func(a, b ledger.Worker) int { return strings.Compare(a.Name, b.Name) })
	out := make([]AssistantWorker, 0, len(workers))
	for _, worker := range workers {
		choice := AssistantWorker{ID: worker.ID, Name: worker.Name}
		host, known := hosts[worker.ID]
		switch {
		case worker.LastHandshakeAt == 0 || now-min(worker.LastHandshakeAt, now) > freshnessBeats*c.heartbeatMs:
			choice.Reason = "is disconnected"
		case !known || !host.Headless:
			choice.Reason = "runs no headless session"
		default:
			for _, repository := range active {
				if !host.Checkouts[repository.ID] {
					choice.Reason = "has no checkout of " + repository.Name
					break
				}
			}
		}
		choice.Available = choice.Reason == ""
		out = append(out, choice)
	}
	return out, nil
}

// joined names things the way a sentence lists them.
func joined(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
}

// place chooses the worker a start runs on. One asked for must be able to; with none asked, the
// conversation's last worker when it still can, else the only one that can. Several that can and none
// asked for is the member's choice to make, and none that can names why for each.
func place(workers []AssistantWorker, asked, last string) (AssistantWorker, error) {
	var available []AssistantWorker
	for _, worker := range workers {
		if asked != "" && (worker.ID == asked || worker.Name == asked) {
			if !worker.Available {
				return AssistantWorker{}, refuseWith(conversations.ErrNoWorker, "%s cannot run the assistant: it %s.", worker.Name, worker.Reason)
			}
			return worker, nil
		}
		if worker.Available {
			available = append(available, worker)
		}
	}
	if asked != "" {
		return AssistantWorker{}, refuseWith(conversations.ErrInvalid, "No worker is named %s.", asked)
	}
	for _, worker := range available {
		if worker.ID == last {
			return worker, nil
		}
	}
	switch len(available) {
	case 1:
		return available[0], nil
	case 0:
		var reasons []string
		for _, worker := range workers {
			reasons = append(reasons, worker.Name+" "+worker.Reason)
		}
		if len(reasons) == 0 {
			return AssistantWorker{}, refuseWith(conversations.ErrNoWorker, "No worker can run the assistant: no worker is registered.")
		}
		return AssistantWorker{}, refuseWith(conversations.ErrNoWorker, "No worker can run the assistant: %s.", strings.Join(reasons, "; "))
	}
	names := make([]string, 0, len(available))
	for _, worker := range available {
		names = append(names, worker.Name)
	}
	return AssistantWorker{}, refuseWith(conversations.ErrInvalid, "Choose a worker: %s can run the assistant.", joined(names))
}

// List reads the calling member's conversations, newest activity first. A delegate reads its member's.
func (c *Conversations) List(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]conversations.Conversation, error) {
	member := memberOf(actor)
	if member == "" {
		return nil, workorders.ErrForbidden
	}
	var out []conversations.Conversation
	err := c.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Conversations().OfMember(member)
		return err
	})
	return out, err
}

// Get reads one of the calling member's conversations.
func (c *Conversations) Get(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (conversations.Conversation, error) {
	var out conversations.Conversation
	err := c.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		out, err = ownConversation(tx.Conversations(), id, memberOf(actor))
		return err
	})
	return out, err
}

// EntryPage is a page of a conversation's entries, oldest first, and the seq a next page reads before.
type EntryPage struct {
	Entries []conversations.Entry
	Next    int64
	AsOf    int64
}

// Entries reads a page of one of the calling member's conversations, before a seq, newest page first.
func (c *Conversations) Entries(ctx context.Context, scope ledger.WorkspaceID, id string, before int64, limit int, actor ledger.Actor) (EntryPage, error) {
	var page EntryPage
	err := c.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		if _, err := ownConversation(tx.Conversations(), id, memberOf(actor)); err != nil {
			return err
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		page.AsOf = now
		rows, err := tx.Conversations().Entries(id, before, limit+1)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			rows = rows[:limit]
			page.Next = rows[len(rows)-1].Seq
		}
		slices.Reverse(rows)
		page.Entries = rows
		return nil
	})
	return page, err
}

// addEntry records an entry at the conversation's next seq, with its event.
func addEntry(tx ports.WriteTx, entry conversations.Entry, actor ledger.Actor) (conversations.Entry, error) {
	seq, err := tx.Conversations().NextSeq(entry.Conversation)
	if err != nil {
		return conversations.Entry{}, err
	}
	entry.Seq, entry.RecordedAt = seq, tx.Now()
	if err := tx.Conversations().AddEntry(entry); err != nil {
		return conversations.Entry{}, err
	}
	event, err := conversations.EntryEvent(entry, actor)
	if err != nil {
		return conversations.Entry{}, err
	}
	_, err = tx.Append(event)
	return entry, err
}

func addMarker(tx ports.WriteTx, record conversations.Conversation, data conversations.MarkerData, actor ledger.Actor) (conversations.Entry, error) {
	id, err := newID(tx.Now())
	if err != nil {
		return conversations.Entry{}, err
	}
	entry, err := conversations.NewEntry(record.ID, id, conversations.KindMarker, "", data)
	if err != nil {
		return conversations.Entry{}, err
	}
	return addEntry(tx, entry, actor)
}

func saveConversation(tx ports.WriteTx, record conversations.Conversation, actor ledger.Actor) error {
	if err := tx.Conversations().Save(record); err != nil {
		return err
	}
	event, err := conversations.ChangedEvent(record, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// stopIn stops a conversation's session with a marker. A session its worker reported running stops on the
// worker's word; one that never reported running has nothing to wait for.
func stopIn(tx ports.WriteTx, record conversations.Conversation, reason, marker string, actor ledger.Actor) (conversations.Conversation, error) {
	if !record.Running() {
		return record, nil
	}
	record.Desired, record.StoppedReason = conversations.DesiredStopped, reason
	if record.Actual == conversations.Running || record.Actual == conversations.Working {
		record.Actual = conversations.Stopping
	} else if record.Actual != conversations.Stopping {
		record.Actual = conversations.Stopped
	}
	if err := saveConversation(tx, record, actor); err != nil {
		return conversations.Conversation{}, err
	}
	_, err := addMarker(tx, record, conversations.MarkerData{Marker: marker}, actor)
	return record, err
}

// stopOthers stops every other running conversation of the member, so at most one runs.
func stopOthers(tx ports.WriteTx, member, keep string, actor ledger.Actor) ([]conversations.Conversation, error) {
	rows, err := tx.Conversations().OfMember(member)
	if err != nil {
		return nil, err
	}
	stopped := []conversations.Conversation{}
	for _, row := range rows {
		if row.ID == keep || !row.Running() {
			continue
		}
		row, err = stopIn(tx, row, conversations.ReasonSwitched, conversations.MarkerStopped, actor)
		if err != nil {
			return nil, err
		}
		stopped = append(stopped, row)
	}
	return stopped, nil
}

// startIn starts a conversation's next generation on a worker under a run lane of its own, with a marker
// that names where the generation begins. A fresh start's session begins with an empty context.
func startIn(tx ports.WriteTx, record conversations.Conversation, worker string, marker string, actor ledger.Actor) (conversations.Conversation, error) {
	lane, err := newID(tx.Now())
	if err != nil {
		return conversations.Conversation{}, err
	}
	record.Desired, record.Actual, record.StoppedReason = conversations.DesiredRunning, conversations.Starting, ""
	record.Worker, record.RunLane, record.Generation, record.LastActiveAt = worker, lane, record.Generation+1, tx.Now()
	if err := saveConversation(tx, record, actor); err != nil {
		return conversations.Conversation{}, err
	}
	_, err = addMarker(tx, record, conversations.MarkerData{Marker: marker, RunLane: lane, Generation: record.Generation}, actor)
	return record, err
}

// Create starts a new conversation for the calling member on a worker that can run it, stopping the
// member's running one first. The client names it, so a retry finds the first attempt.
func (c *Conversations) Create(ctx context.Context, scope ledger.WorkspaceID, in NewConversation, actor ledger.Actor) (Opened, error) {
	if err := changing(actor); err != nil {
		return Opened{}, err
	}
	if err := conversations.CheckID(in.ID); err != nil {
		return Opened{}, err
	}
	name := conversations.DefaultName
	if in.Name != "" {
		var err error
		if name, err = conversations.CheckName(in.Name); err != nil {
			return Opened{}, err
		}
	}
	workers, err := c.Workers(ctx, scope)
	if err != nil {
		return Opened{}, err
	}
	var opened Opened
	err = c.store.Write(ctx, scope, opConversationWrite, func(tx ports.WriteTx) error {
		existing, err := tx.Conversations().Conversation(in.ID)
		if err == nil {
			if existing.Member != actor.ID {
				return refuseWith(conversations.ErrInvalid, "Conversation ID %s is taken.", in.ID)
			}
			opened = Opened{Conversation: existing, Stopped: []conversations.Conversation{}}
			return nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		worker, err := place(workers, in.Worker, "")
		if err != nil {
			return err
		}
		if opened.Stopped, err = stopOthers(tx, actor.ID, in.ID, actor); err != nil {
			return err
		}
		record := conversations.Conversation{
			ID: in.ID, Member: actor.ID, Name: name, Desired: conversations.DesiredStopped, Actual: conversations.Stopped,
			CreatedAt: tx.Now(), LastActiveAt: tx.Now(),
		}
		if err := tx.Conversations().Insert(record); err != nil {
			return err
		}
		opened.Conversation, err = startIn(tx, record, worker.ID, conversations.MarkerStarted, actor)
		return err
	})
	if err != nil {
		return Opened{}, err
	}
	c.revokeAll(ctx, scope, opened.Stopped, authority.DelegateEndStopped, actor)
	return opened, nil
}

// Open starts the session of one of the calling member's conversations, resuming what it had, and stops
// the member's running one first. A conversation already running stays as it is.
func (c *Conversations) Open(ctx context.Context, scope ledger.WorkspaceID, id, worker string, actor ledger.Actor) (Opened, error) {
	if err := changing(actor); err != nil {
		return Opened{}, err
	}
	workers, err := c.Workers(ctx, scope)
	if err != nil {
		return Opened{}, err
	}
	var opened Opened
	err = c.store.Write(ctx, scope, opConversationWrite, func(tx ports.WriteTx) error {
		var err error
		opened, err = openIn(tx, workers, id, worker, actor)
		return err
	})
	if err != nil {
		return Opened{}, err
	}
	c.revokeAll(ctx, scope, opened.Stopped, authority.DelegateEndStopped, actor)
	return opened, nil
}

func openIn(tx ports.WriteTx, workers []AssistantWorker, id, asked string, actor ledger.Actor) (Opened, error) {
	record, err := ownConversation(tx.Conversations(), id, actor.ID)
	if err != nil {
		return Opened{}, err
	}
	opened := Opened{Conversation: record, Stopped: []conversations.Conversation{}}
	if record.Running() {
		return opened, nil
	}
	worker, err := place(workers, asked, record.Worker)
	if err != nil {
		return Opened{}, err
	}
	if opened.Stopped, err = stopOthers(tx, actor.ID, id, actor); err != nil {
		return Opened{}, err
	}
	opened.Conversation, err = startIn(tx, record, worker.ID, conversations.MarkerStarted, actor)
	return opened, err
}

// Stop stops the session of one of the calling member's conversations. The conversation stays, and
// resumes when the member sends.
func (c *Conversations) Stop(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (conversations.Conversation, error) {
	return c.stop(ctx, scope, id, conversations.ReasonMember, conversations.MarkerStopped, actor)
}

func (c *Conversations) stop(ctx context.Context, scope ledger.WorkspaceID, id, reason, marker string, actor ledger.Actor) (conversations.Conversation, error) {
	if err := changing(actor); err != nil {
		return conversations.Conversation{}, err
	}
	var record conversations.Conversation
	err := c.store.Write(ctx, scope, opConversationWrite, func(tx ports.WriteTx) error {
		var err error
		if record, err = ownConversation(tx.Conversations(), id, actor.ID); err != nil {
			return err
		}
		record, err = stopIn(tx, record, reason, marker, actor)
		return err
	})
	if err != nil {
		return conversations.Conversation{}, err
	}
	c.revokeAll(ctx, scope, []conversations.Conversation{record}, authority.DelegateEndStopped, actor)
	return record, nil
}

// Rename names one of the calling member's conversations.
func (c *Conversations) Rename(ctx context.Context, scope ledger.WorkspaceID, id, name string, actor ledger.Actor) (conversations.Conversation, error) {
	if err := changing(actor); err != nil {
		return conversations.Conversation{}, err
	}
	name, err := conversations.CheckName(name)
	if err != nil {
		return conversations.Conversation{}, err
	}
	var record conversations.Conversation
	err = c.store.Write(ctx, scope, opConversationWrite, func(tx ports.WriteTx) error {
		var err error
		if record, err = ownConversation(tx.Conversations(), id, actor.ID); err != nil {
			return err
		}
		record.Name = name
		return saveConversation(tx, record, actor)
	})
	return record, err
}

// Delete stops one of the calling member's conversations and removes it with every entry and message it
// holds, in one transaction. What its actions did, and the cards it prepared, stay in the ledger.
func (c *Conversations) Delete(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) error {
	if err := changing(actor); err != nil {
		return err
	}
	var record conversations.Conversation
	err := c.store.Write(ctx, scope, opConversationWrite, func(tx ports.WriteTx) error {
		var err error
		if record, err = ownConversation(tx.Conversations(), id, actor.ID); err != nil {
			return err
		}
		if record.Running() {
			record.Desired, record.Actual, record.StoppedReason = conversations.DesiredStopped, conversations.Stopped, conversations.ReasonDeleted
			if err := saveConversation(tx, record, actor); err != nil {
				return err
			}
		}
		if _, err := tx.Messages().DropTo(messages.TargetAssistant, id); err != nil {
			return err
		}
		if err := tx.Conversations().Delete(id); err != nil {
			return err
		}
		event, err := conversations.DeletedEvent(record, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		return err
	}
	c.revokeAll(ctx, scope, []conversations.Conversation{record}, authority.DelegateEndStopped, actor)
	return nil
}

// Compact asks the running session of one of the calling member's conversations to compact its context
// with the runtime's own command, and marks where it did.
func (c *Conversations) Compact(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (conversations.Conversation, error) {
	if err := changing(actor); err != nil {
		return conversations.Conversation{}, err
	}
	var record conversations.Conversation
	err := c.store.Write(ctx, scope, opConversationWrite, func(tx ports.WriteTx) error {
		var err error
		if record, err = ownConversation(tx.Conversations(), id, actor.ID); err != nil {
			return err
		}
		if !record.Live() {
			return refuseWith(conversations.ErrNotRunning, "The assistant in %q is not running. Resume it first.", record.Name)
		}
		_, err = addMarker(tx, record, conversations.MarkerData{Marker: conversations.MarkerCompacted}, actor)
		return err
	})
	return record, err
}

// Clear ends the running generation of one of the calling member's conversations and starts the next with
// an empty context in the same conversation, on the same worker. Every earlier entry stays readable, marked
// as before the clear.
func (c *Conversations) Clear(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (conversations.Conversation, error) {
	if err := changing(actor); err != nil {
		return conversations.Conversation{}, err
	}
	var record, before conversations.Conversation
	err := c.store.Write(ctx, scope, opConversationWrite, func(tx ports.WriteTx) error {
		var err error
		if record, err = ownConversation(tx.Conversations(), id, actor.ID); err != nil {
			return err
		}
		if !record.Live() {
			return refuseWith(conversations.ErrNotRunning, "The assistant in %q is not running. Resume it first.", record.Name)
		}
		before = record
		last, err := tx.Conversations().NextSeq(id)
		if err != nil {
			return err
		}
		if err := tx.Conversations().MarkBeforeClear(id, last-1); err != nil {
			return err
		}
		record, err = startIn(tx, record, record.Worker, conversations.MarkerCleared, actor)
		return err
	})
	if err != nil {
		return conversations.Conversation{}, err
	}
	c.revoke(ctx, scope, before.ID, record.Generation, authority.DelegateEndGeneration, actor)
	return record, nil
}

// MemberMessage is a member's message to the assistant of one of their conversations. ID is the client's.
type MemberMessage struct {
	ID     string
	Body   string
	Worker string
}

// Send records a member's message to the assistant, opening a stopped conversation first, and names the
// conversation with the message when it has no name of the member's. The message is never consent: a
// message that reads as a yes while a card waits is marked so, and the card waits for a member's click.
func (c *Conversations) Send(ctx context.Context, scope ledger.WorkspaceID, id string, in MemberMessage, actor ledger.Actor) (Sent, error) {
	if err := changing(actor); err != nil {
		return Sent{}, err
	}
	if _, err := messages.New(messages.Draft{ID: in.ID, To: messages.To(messages.TargetAssistant, id, 0), Kind: messages.KindInstruction, Body: in.Body}, messages.Sender{}, 0, messages.DefaultWindow); err != nil {
		return Sent{}, err
	}
	workers, err := c.Workers(ctx, scope)
	if err != nil {
		return Sent{}, err
	}
	var sent Sent
	err = c.store.Write(ctx, scope, opConversationWrite, func(tx ports.WriteTx) error {
		if existing, err := tx.Messages().Message(in.ID); err == nil {
			return c.resent(tx, &sent, existing, actor)
		} else if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		opened, err := openIn(tx, workers, id, in.Worker, actor)
		if err != nil {
			return err
		}
		record, stopped := opened.Conversation, opened.Stopped
		pending, err := cardPending(tx.Members(), tx.Approvals(), record.ID)
		if err != nil {
			return err
		}
		seq, err := tx.Messages().NextSeq(string(actor.Kind), actor.ID)
		if err != nil {
			return err
		}
		message, err := messages.New(messages.Draft{
			ID: in.ID, To: messages.To(messages.TargetAssistant, record.ID, record.Generation), Kind: messages.KindInstruction, Body: in.Body,
		}, messages.Sender{Kind: string(actor.Kind), ID: actor.ID, Seq: seq}, tx.Now(), c.windows.For(messages.KindInstruction))
		if err != nil {
			return err
		}
		if err := tx.Messages().Insert(message); err != nil {
			return err
		}
		if err := appendMessage(tx, message, actor, noStamp); err != nil {
			return err
		}
		first, err := firstMemberEntry(tx, record.ID)
		if err != nil {
			return err
		}
		entry, err := conversations.NewEntry(record.ID, in.ID, conversations.KindMember, in.Body,
			conversations.MemberData{Message: in.ID, Assent: conversations.Assent(in.Body, pending)})
		if err != nil {
			return err
		}
		if entry, err = addEntry(tx, entry, actor); err != nil {
			return err
		}
		if first && record.Name == conversations.DefaultName {
			record.Name = conversations.NameFrom(in.Body)
		}
		record.LastActiveAt = tx.Now()
		if err := saveConversation(tx, record, actor); err != nil {
			return err
		}
		sent = Sent{Conversation: record, Entry: entry, Message: message, Stopped: stopped}
		return nil
	})
	if err != nil {
		return Sent{}, err
	}
	c.revokeAll(ctx, scope, sent.Stopped, authority.DelegateEndStopped, actor)
	return sent, nil
}

// resent reads what a retry's first attempt recorded.
func (c *Conversations) resent(tx ports.WriteTx, sent *Sent, existing messages.Message, actor ledger.Actor) error {
	if existing.To.Of() != messages.TargetAssistant || existing.From.ID != actor.ID {
		return refuseWith(messages.ErrInvalid, "Message ID %s is taken.", existing.ID)
	}
	record, err := ownConversation(tx.Conversations(), existing.To.Conversation, actor.ID)
	if err != nil {
		return err
	}
	entry, err := tx.Conversations().Entry(record.ID, existing.ID)
	if err != nil {
		return err
	}
	*sent = Sent{Conversation: record, Entry: entry, Message: existing, Stopped: []conversations.Conversation{}}
	return nil
}

func firstMemberEntry(tx ports.WriteTx, conversation string) (bool, error) {
	before := int64(0)
	for {
		rows, err := tx.Conversations().Entries(conversation, before, 200)
		if err != nil || len(rows) == 0 {
			return true, err
		}
		for _, row := range rows {
			if row.Kind == conversations.KindMember {
				return false, nil
			}
		}
		before = rows[len(rows)-1].Seq
	}
}

// cardPending says a card the assistant prepared in this conversation waits for the member.
func cardPending(members ports.MemberReader, approvals ports.ApprovalReader, conversation string) (bool, error) {
	cards, err := conversationCards(members, approvals, conversation)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(cards, func(card authority.Approval) bool { return card.State == authority.ApprovalPending }), nil
}

// conversationCards lists the cards the assistant prepared under the credentials of one conversation's
// sessions, oldest first.
func conversationCards(members ports.MemberReader, approvals ports.ApprovalReader, conversation string) ([]authority.Approval, error) {
	credentials, err := members.ConversationDelegates(conversation)
	if err != nil || len(credentials) == 0 {
		return nil, err
	}
	principals := map[string]bool{}
	for _, credential := range credentials {
		principals[authority.Principal(credential.Actor())] = true
	}
	all, err := approvals.All()
	if err != nil {
		return nil, err
	}
	var out []authority.Approval
	for _, row := range all {
		if row.Kind == authority.KindConfirm && principals[row.RequestedBy] {
			out = append(out, row)
		}
	}
	return out, nil
}

// IdleStop stops every running conversation whose last activity is older than the assistant policy's idle
// stop, with an idle marker. Closing a view or switching target stops nothing: only this does.
func (c *Conversations) IdleStop(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error) {
	var due []string
	err := c.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		policy, err := assistantInForce(tx.Policies())
		if err != nil {
			return err
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		running, err := tx.Conversations().Running()
		if err != nil {
			return err
		}
		for _, row := range running {
			if now-row.LastActiveAt > policy.IdleStopAfter().Milliseconds() {
				due = append(due, row.ID)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var stopped []string
	for _, id := range due {
		var record conversations.Conversation
		moved := false
		err := c.store.Write(ctx, scope, opConversationIdle, func(tx ports.WriteTx) error {
			row, err := tx.Conversations().Conversation(id)
			if errors.Is(err, ports.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			policy, err := assistantInForce(tx.Policies())
			if err != nil {
				return err
			}
			if !row.Running() || tx.Now()-row.LastActiveAt <= policy.IdleStopAfter().Milliseconds() {
				return nil
			}
			record, err = stopIn(tx, row, conversations.ReasonIdle, conversations.MarkerIdleStopped, actor)
			moved = err == nil
			return err
		})
		if err != nil {
			return stopped, err
		}
		if moved {
			stopped = append(stopped, id)
			c.revokeAll(ctx, scope, []conversations.Conversation{record}, authority.DelegateEndIdle, actor)
		}
	}
	return stopped, nil
}

// StopMember stops every running conversation of a member, as signing out everywhere does.
func (c *Conversations) StopMember(ctx context.Context, scope ledger.WorkspaceID, member string, actor ledger.Actor) ([]string, error) {
	var stopped []conversations.Conversation
	err := c.store.Write(ctx, scope, opConversationWrite, func(tx ports.WriteTx) error {
		stopped = nil
		rows, err := tx.Conversations().OfMember(member)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if !row.Running() {
				continue
			}
			row, err = stopIn(tx, row, conversations.ReasonSignedOut, conversations.MarkerStopped, actor)
			if err != nil {
				return err
			}
			stopped = append(stopped, row)
		}
		if len(stopped) == 0 {
			return errNothingToStop
		}
		return nil
	})
	if errors.Is(err, errNothingToStop) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.revokeAll(ctx, scope, stopped, authority.DelegateEndSignedOut, actor)
	ids := make([]string, 0, len(stopped))
	for _, row := range stopped {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

// errNothingToStop rolls back a transaction that found nothing to change.
var errNothingToStop = errors.New("nothing to stop")

// revokeAll ends every credential of each conversation's sessions, for a reason.
func (c *Conversations) revokeAll(ctx context.Context, scope ledger.WorkspaceID, rows []conversations.Conversation, reason string, actor ledger.Actor) {
	for _, row := range rows {
		c.revoke(ctx, scope, row.ID, 0, reason, actor)
	}
}

// revoke ends the credentials of a conversation's sessions below a generation, and every one of them when
// it is zero. A failure leaves a credential its generation no longer passes, so the gate still refuses it.
func (c *Conversations) revoke(ctx context.Context, scope ledger.WorkspaceID, conversation string, below int64, reason string, actor ledger.Actor) {
	_ = revokeAssistant(ctx, c.store.Installation(scope), conversation, below, reason, actor)
}

// revokeAssistant ends the active credentials of a conversation's sessions below a generation, or every one
// when below is zero, for a reason, in one installation transaction with an event for each.
func revokeAssistant(ctx context.Context, installation ports.Installation, conversation string, below int64, reason string, actor ledger.Actor) error {
	err := installation.Write(ctx, "delegate.revoke", func(tx ports.SharedTx) error {
		credentials, err := tx.Members().ConversationDelegates(conversation)
		if err != nil {
			return err
		}
		revoked := 0
		for _, credential := range credentials {
			if credential.State(tx.Now()) != authority.DelegateActive || (below != 0 && credential.Generation >= below) {
				continue
			}
			done, err := tx.Members().RevokeDelegate(credential.ID, tx.Now(), reason)
			if err != nil {
				return err
			}
			if !done {
				continue
			}
			credential.RevokedAt, credential.EndedReason = tx.Now(), reason
			if err := appendDelegate(tx, EventDelegateRevoked, credential, actor); err != nil {
				return err
			}
			revoked++
		}
		if revoked == 0 {
			return errNothingToStop
		}
		return nil
	})
	if errors.Is(err, errNothingToStop) {
		return nil
	}
	return err
}

// ConversationCards lists the cards the assistant prepared in one conversation, oldest first.
func ConversationCards(members ports.MemberReader, approvals ports.ApprovalReader, conversation string) ([]authority.Approval, error) {
	return conversationCards(members, approvals, conversation)
}

// RecordAction adds what a call of an assistant's credential did to the conversation the credential acts in,
// with the newest project event before it. A conversation that is gone, or is another member's, records
// nothing.
func (c *Conversations) RecordAction(ctx context.Context, scope ledger.WorkspaceID, credential authority.DelegateCredential, action conversations.ActionData) error {
	if !credential.Assistant() {
		return nil
	}
	actor := credential.Actor()
	err := c.store.Write(ctx, scope, opConversationWrite, func(tx ports.WriteTx) error {
		record, err := tx.Conversations().Conversation(credential.Conversation)
		if errors.Is(err, ports.ErrNotFound) {
			return errNothingToStop
		}
		if err != nil {
			return err
		}
		if record.Member != credential.Member {
			return errNothingToStop
		}
		workspace, err := tx.Workspace()
		if err != nil {
			return err
		}
		action.Rev = workspace.Rev
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		entry, err := conversations.NewEntry(record.ID, id, conversations.KindAction, "", action)
		if err != nil {
			return err
		}
		_, err = addEntry(tx, entry, actor)
		return err
	})
	if errors.Is(err, errNothingToStop) {
		return nil
	}
	return err
}
