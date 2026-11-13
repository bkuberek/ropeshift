package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
)

// ConversationChecks are what the ledger promises a member's conversations with the assistant (ADR-0024):
// one runs at a time per member and project, a conversation is its member's alone, what anyone wrote lives
// only where deleting the conversation removes it, an assistant credential acts only while its generation
// runs, and a message never decides anything (FR-MSG-6).
var ConversationChecks = []Check{
	{"CV1", "at most one of a member's conversations runs, and starting or opening another stops it first", checkOneConversationRuns},
	{"CV2", "the first member message names a conversation, unless the member named it", checkConversationNamed},
	{"CV3", "another member's conversation reads as not found, and no delegate changes one", checkConversationIsTheMembers},
	{"CV4", "deleting a conversation removes every body it holds in one transaction, and its events stay", checkConversationDeleted},
	{"CV5", "no event of a conversation, its entries, or a message to the assistant holds a body or a name", checkConversationEventsHoldNoBody},
	{"CV6", "compact and clear need a running session, and a clear marks every earlier entry", checkConversationCompactClear},
	{"CV7", "the tick stops a conversation idle past the assistant policy, and nothing younger", checkConversationIdleStop},
	{"CV8", "a message to a stopped conversation opens it and waits for its session", checkConversationMessageOpens},
	{"CV9", "an assistant credential acts only while its generation runs", checkAssistantCredential},
	{"CV10", "a reply is recorded once, as activity", checkAssistantReplyOnce},
	{"CV11", "a message that reads as assent leaves the card open", checkAssentDecidesNothing},
	{"CV12", "a run's states move its conversation, and its end stops it", checkAssistantRunStates},
	{"CV13", "desired state names each running conversation on its worker, with its messages", checkAssistantDesired},
	{"CV14", "a worker can run the assistant only when connected, headless, and holding every repository", checkAssistantPlacement},
	{"CV15", "a message to an orchestrator is refused unless it runs and serves the project, and walks its states as a wake item", checkOrchestratorMessages},
	{"CV16", "an assistant credential's end says why: a routine end never overwrites a member's, and a member's revocation or sign-out overwrites a routine one", checkAssistantCredentialEnd},
	{"CV17", "a conversation ID names one conversation of its project for good, and its credentials are its member's in its project alone", checkConversationIDScoped},
}

var theOtherMember = ledger.Actor{Kind: ledger.ActorMember, ID: "member-2"}

// talkRig is a project with one repository and one worker that can run the assistant on it.
type talkRig struct {
	r      *Rig
	store  ports.Store
	plane  *control.Workers
	convs  *control.Conversations
	auth   ledger.WorkerAuth
	epoch  int64
	seq    int64
	repo   string
	hosts  map[string]control.AssistantHost
	epochs map[string]int64
}

func newTalkRig(r *Rig) (*talkRig, error) {
	t := &talkRig{r: r, store: r.Store(), hosts: map[string]control.AssistantHost{}, epochs: map[string]int64{}}
	t.plane = control.NewWorkers(t.store, r.Scope, control.WorkerOptions{HeartbeatMs: statusHeartbeatMs, LeaseTTLMs: TTLMs})
	worker, err := t.register("atlas")
	if err != nil {
		return nil, err
	}
	t.auth, t.epoch = worker, t.epochs[worker.WorkerID]
	repository, err := control.NewRepositories(t.store).Detect(r.Ctx, r.Scope, control.DetectedRepository{
		ID: r.ID("repo"), Directory: "harbor-api", DefaultBranch: "main",
	}, theMember)
	if err != nil {
		return nil, err
	}
	t.repo = repository.ID
	t.hosts[worker.WorkerID] = control.AssistantHost{Headless: true, Checkouts: map[string]bool{t.repo: true}}
	t.convs = control.NewConversations(t.store, nil).Hosting(t.hostsOf, statusHeartbeatMs)
	return t, nil
}

func (t *talkRig) hostsOf(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
	return t.hosts, nil
}

// register adds a connected worker by name.
func (t *talkRig) register(name string) (ledger.WorkerAuth, error) {
	token, err := t.plane.CreateJoinToken(t.r.Ctx, theMember)
	if err != nil {
		return ledger.WorkerAuth{}, err
	}
	id := t.r.ID("worker")
	credential, err := t.plane.Register(t.r.Ctx, ledger.Registration{JoinToken: token, WorkerID: id, Name: name})
	if err != nil {
		return ledger.WorkerAuth{}, err
	}
	auth := ledger.WorkerAuth{WorkerID: id, Credential: credential.Credential}
	session, err := t.plane.OpenSession(t.r.Ctx, ledger.SessionRequest{Auth: auth, Protocol: ledger.ProtocolVersion, Manifest: UsableManifest})
	t.epochs[id] = session.Epoch
	return auth, err
}

func (t *talkRig) create(actor ledger.Actor, name string) (control.Opened, error) {
	return t.convs.Create(t.r.Ctx, t.r.Scope, control.NewConversation{ID: t.r.ID("conv"), Name: name}, actor)
}

func (t *talkRig) send(actor ledger.Actor, id, body string) (control.Sent, error) {
	return t.convs.Send(t.r.Ctx, t.r.Scope, id, control.MemberMessage{ID: t.r.ID("msg"), Body: body}, actor)
}

func (t *talkRig) read(id string) (conversations.Conversation, error) {
	return t.convs.Get(t.r.Ctx, t.r.Scope, id, theMember)
}

func (t *talkRig) entries(id string) ([]conversations.Entry, error) {
	page, err := t.convs.Entries(t.r.Ctx, t.r.Scope, id, 0, 500, theMember)
	return page.Entries, err
}

// markers lists a conversation's markers in order.
func (t *talkRig) markers(id string) ([]string, error) {
	rows, err := t.entries(id)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, row := range rows {
		if row.Kind == conversations.KindMarker {
			marker, err := row.Marker()
			if err != nil {
				return nil, err
			}
			out = append(out, marker)
		}
	}
	return out, nil
}

// runUpload sends what the worker says of a conversation's run at one of the run's generations.
func (t *talkRig) runUpload(record conversations.Conversation, eventType string, generation int64, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	t.seq++
	reply, err := t.plane.Upload(t.r.Ctx, ledger.UploadBatch{Auth: t.auth, Epoch: t.epoch, Events: []ledger.UploadEvent{{
		Epoch: t.epoch, Seq: t.seq, Type: eventType, Lane: record.RunLane, Generation: generation, Payload: raw, OccurredAt: 1,
	}}})
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 0 {
		return Violatedf("the ledger refused %s: %+v", eventType, reply.Rejected)
	}
	return nil
}

// running reports a conversation's session at rest, waiting for its member.
func (t *talkRig) running(record conversations.Conversation) (conversations.Conversation, error) {
	if err := t.runUpload(record, ledger.UploadLaneStatus, 1, ledger.LaneStatus{ActualState: "idle", Liveness: "alive"}); err != nil {
		return conversations.Conversation{}, err
	}
	return t.read(record.ID)
}

func checkOneConversationRuns(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	first, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	if !first.Conversation.Running() || first.Conversation.Actual != conversations.Starting || len(first.Stopped) != 0 {
		return Violatedf("a first conversation reads %+v", first)
	}
	theirs, err := t.create(theOtherMember, "")
	if err != nil {
		return err
	}
	second, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	if len(second.Stopped) != 1 || second.Stopped[0].ID != first.Conversation.ID {
		return Violatedf("a second conversation names %+v as stopped", second.Stopped)
	}
	again, err := t.read(first.Conversation.ID)
	if err != nil {
		return err
	}
	if again.Running() || again.StoppedReason != conversations.ReasonSwitched {
		return Violatedf("the first conversation still reads %+v", again)
	}
	if markers, err := t.markers(first.Conversation.ID); err != nil || strings.Join(markers, ",") != "started,stopped" {
		return Violatedf("its markers are %v (%v)", markers, err)
	}
	reopened, err := t.convs.Open(r.Ctx, r.Scope, first.Conversation.ID, "", theMember)
	if err != nil {
		return err
	}
	if len(reopened.Stopped) != 1 || reopened.Stopped[0].ID != second.Conversation.ID || reopened.Conversation.Generation != 2 {
		return Violatedf("opening the first again reads %+v", reopened)
	}
	if reopened.Conversation.RunLane == first.Conversation.RunLane {
		return Violatedf("a second start reused run lane %s", first.Conversation.RunLane)
	}
	rows, err := t.convs.List(r.Ctx, r.Scope, theMember)
	if err != nil {
		return err
	}
	running := 0
	for _, row := range rows {
		if row.Running() {
			running++
		}
	}
	if running != 1 {
		return Violatedf("%d of the member's conversations run", running)
	}
	other, err := t.convs.Get(r.Ctx, r.Scope, theirs.Conversation.ID, theOtherMember)
	if err != nil {
		return err
	}
	if !other.Running() {
		return Violatedf("another member's conversation stopped: %+v", other)
	}
	return nil
}

func checkConversationNamed(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	if opened.Conversation.Name != conversations.DefaultName {
		return Violatedf("a new conversation is called %q", opened.Conversation.Name)
	}
	if _, err := t.send(theMember, opened.Conversation.ID, "Why did the harbor-web merge fail last night after the rebase?"); err != nil {
		return err
	}
	if _, err := t.send(theMember, opened.Conversation.ID, "Something else entirely"); err != nil {
		return err
	}
	named, err := t.read(opened.Conversation.ID)
	if err != nil {
		return err
	}
	if named.Name != "Why did the harbor-web merge fail last night after the" {
		return Violatedf("the first message named it %q", named.Name)
	}
	given, err := t.create(theMember, "Merges")
	if err != nil {
		return err
	}
	if _, err := t.send(theMember, given.Conversation.ID, "Which merges wait?"); err != nil {
		return err
	}
	if kept, err := t.read(given.Conversation.ID); err != nil || kept.Name != "Merges" {
		return Violatedf("a member's name became %q (%v)", kept.Name, err)
	}
	renamed, err := t.convs.Rename(r.Ctx, r.Scope, given.Conversation.ID, "  Merge queue  ", theMember)
	if err != nil || renamed.Name != "Merge queue" {
		return Violatedf("rename gave %q (%v)", renamed.Name, err)
	}
	if _, err := t.convs.Rename(r.Ctx, r.Scope, given.Conversation.ID, strings.Repeat("x", 81), theMember); !errors.Is(err, conversations.ErrInvalid) {
		return Violatedf("an 81-character name: %v", err)
	}
	return nil
}

func checkConversationIsTheMembers(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	id := opened.Conversation.ID
	attempts := map[string]error{}
	_, attempts["get"] = t.convs.Get(r.Ctx, r.Scope, id, theOtherMember)
	_, attempts["open"] = t.convs.Open(r.Ctx, r.Scope, id, "", theOtherMember)
	_, attempts["stop"] = t.convs.Stop(r.Ctx, r.Scope, id, theOtherMember)
	_, attempts["rename"] = t.convs.Rename(r.Ctx, r.Scope, id, "Mine", theOtherMember)
	attempts["delete"] = t.convs.Delete(r.Ctx, r.Scope, id, theOtherMember)
	_, attempts["compact"] = t.convs.Compact(r.Ctx, r.Scope, id, theOtherMember)
	_, attempts["clear"] = t.convs.Clear(r.Ctx, r.Scope, id, theOtherMember)
	_, attempts["send"] = t.send(theOtherMember, id, "hello")
	_, attempts["entries"] = t.convs.Entries(r.Ctx, r.Scope, id, 0, 10, theOtherMember)
	for what, err := range attempts {
		if !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("another member's %s: %v", what, err)
		}
	}
	delegate := authority.DelegateActor(theMember.ID, "dlg_x")
	if _, err := t.convs.Get(r.Ctx, r.Scope, id, delegate); err != nil {
		return Violatedf("a delegate cannot read its member's conversation: %v", err)
	}
	if _, err := t.convs.Stop(r.Ctx, r.Scope, id, delegate); !errors.Is(err, authority.ErrMemberOnly) {
		return Violatedf("a delegate stopped its member's conversation: %v", err)
	}
	if _, err := t.send(delegate, id, "yes"); !errors.Is(err, authority.ErrMemberOnly) {
		return Violatedf("a delegate wrote as its member: %v", err)
	}
	return nil
}

// conversationEvents lists every event whose subject is a conversation or a message to the assistant.
func (t *talkRig) events() ([]ledger.Event, error) {
	return t.r.Events(t.store, t.r.Scope)
}

func checkConversationDeleted(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	id := opened.Conversation.ID
	sent, err := t.send(theMember, id, "The launch codes are 1234")
	if err != nil {
		return err
	}
	before, err := t.events()
	if err != nil {
		return err
	}
	if err := t.convs.Delete(r.Ctx, r.Scope, id, theMember); err != nil {
		return err
	}
	if _, err := t.read(id); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a deleted conversation reads: %v", err)
	}
	err = t.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		if _, err := tx.Messages().Message(sent.Message.ID); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("its message stays: %v", err)
		}
		rows, err := tx.Conversations().Entries(id, 0, 100)
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			return Violatedf("%d entries stay", len(rows))
		}
		return nil
	})
	if err != nil {
		return err
	}
	after, err := t.events()
	if err != nil {
		return err
	}
	if len(after) <= len(before) || after[len(after)-1].Type != conversations.EventDeleted {
		return Violatedf("the last event is %s", after[len(after)-1].Type)
	}
	for _, event := range after {
		if strings.Contains(string(event.Data), "launch codes") {
			return Violatedf("event %s holds the body", event.Type)
		}
	}
	kept := 0
	for _, event := range after {
		if event.Subject.Kind == conversations.SubjectConversation && event.Subject.ID == id {
			kept++
		}
	}
	if kept < 4 {
		return Violatedf("%d events of the conversation stay", kept)
	}
	return nil
}

func checkConversationEventsHoldNoBody(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	running, err := t.running(opened.Conversation)
	if err != nil {
		return err
	}
	if _, err := t.send(theMember, running.ID, "Secretive harbor plans for the quarter"); err != nil {
		return err
	}
	if err := t.plane.AssistantEntry(r.Ctx, ledger.AssistantEntryRequest{
		Auth: t.auth, Epoch: t.epoch, Conversation: running.ID, Lane: running.RunLane, ID: "msg_vendor_1", Body: "A confidential answer",
	}); err != nil {
		return err
	}
	events, err := t.events()
	if err != nil {
		return err
	}
	for _, event := range events {
		data := string(event.Data)
		if strings.Contains(data, "Secretive") || strings.Contains(data, "confidential") {
			return Violatedf("event %s holds %s", event.Type, data)
		}
	}
	return nil
}

func checkConversationCompactClear(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	id := opened.Conversation.ID
	if _, err := t.convs.Compact(r.Ctx, r.Scope, id, theMember); !errors.Is(err, conversations.ErrNotRunning) {
		return Violatedf("compacting a session that never ran: %v", err)
	}
	record, err := t.running(opened.Conversation)
	if err != nil {
		return err
	}
	if record.Actual != conversations.Running {
		return Violatedf("a run at rest reads %s", record.Actual)
	}
	if _, err := t.send(theMember, id, "First question"); err != nil {
		return err
	}
	if _, err := t.convs.Compact(r.Ctx, r.Scope, id, theMember); err != nil {
		return err
	}
	cleared, err := t.convs.Clear(r.Ctx, r.Scope, id, theMember)
	if err != nil {
		return err
	}
	if cleared.Generation != record.Generation+1 || cleared.RunLane == record.RunLane || cleared.Actual != conversations.Starting {
		return Violatedf("a clear left %+v", cleared)
	}
	rows, err := t.entries(id)
	if err != nil {
		return err
	}
	last := rows[len(rows)-1]
	if marker, _ := last.Marker(); marker != conversations.MarkerCleared || last.BeforeClear {
		return Violatedf("the clear's marker reads %+v", last)
	}
	for _, row := range rows[:len(rows)-1] {
		if !row.BeforeClear {
			return Violatedf("entry %d is not marked as before the clear", row.Seq)
		}
	}
	if markers, err := t.markers(id); err != nil || strings.Join(markers, ",") != "started,compacted,cleared" {
		return Violatedf("markers %v (%v)", markers, err)
	}
	// The next generation starts with an empty context: its first prompt holds nothing from before the clear.
	if _, err := t.send(theMember, id, "Second question"); err != nil {
		return err
	}
	grant, err := t.plane.AssistantCredential(r.Ctx, ledger.AssistantCredentialRequest{
		Auth: t.auth, Epoch: t.epoch, Conversation: id, Lane: cleared.RunLane, Generation: 1,
	})
	if err != nil {
		return err
	}
	var history []string
	for _, entry := range grant.History {
		history = append(history, entry.Body)
	}
	if text := strings.Join(history, "|"); strings.Contains(text, "First question") || !strings.Contains(text, "Second question") {
		return Violatedf("the first prompt after a clear holds %q", text)
	}
	if _, err := t.convs.Stop(r.Ctx, r.Scope, id, theMember); err != nil {
		return err
	}
	if _, err := t.convs.Clear(r.Ctx, r.Scope, id, theMember); !errors.Is(err, conversations.ErrNotRunning) {
		return Violatedf("clearing a stopped conversation: %v", err)
	}
	if _, err := t.convs.Compact(r.Ctx, r.Scope, id, theMember); !errors.Is(err, conversations.ErrNotRunning) {
		return Violatedf("compacting a stopped conversation: %v", err)
	}
	return nil
}

func checkConversationIdleStop(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	idle, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	tick := ledger.Actor{Kind: ledger.ActorSystem, ID: "tick"}
	r.DB.ShiftClock(10 * 60 * 1000)
	if _, err := t.plane.OpenSession(r.Ctx, ledger.SessionRequest{Auth: t.auth, Protocol: ledger.ProtocolVersion, Manifest: UsableManifest}); err != nil {
		return err
	}
	young, err := t.create(theOtherMember, "")
	if err != nil {
		return err
	}
	stopped, err := t.convs.IdleStop(r.Ctx, r.Scope, tick)
	if err != nil {
		return err
	}
	if len(stopped) != 0 {
		return Violatedf("the tick stopped %v inside the built-in fifteen minutes", stopped)
	}
	r.DB.ShiftClock(6 * 60 * 1000)
	if stopped, err = t.convs.IdleStop(r.Ctx, r.Scope, tick); err != nil {
		return err
	}
	if len(stopped) != 1 || stopped[0] != idle.Conversation.ID {
		return Violatedf("the tick stopped %v", stopped)
	}
	record, err := t.read(idle.Conversation.ID)
	if err != nil {
		return err
	}
	if record.Running() || record.StoppedReason != conversations.ReasonIdle {
		return Violatedf("an idle conversation reads %+v", record)
	}
	if markers, err := t.markers(idle.Conversation.ID); err != nil || markers[len(markers)-1] != conversations.MarkerIdleStopped {
		return Violatedf("markers %v (%v)", markers, err)
	}
	other, err := t.convs.Get(r.Ctx, r.Scope, young.Conversation.ID, theOtherMember)
	if err != nil || !other.Running() {
		return Violatedf("a younger conversation reads %+v (%v)", other, err)
	}
	return nil
}

func checkConversationMessageOpens(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	stopped, err := t.convs.Stop(r.Ctx, r.Scope, opened.Conversation.ID, theMember)
	if err != nil {
		return err
	}
	if stopped.Running() || stopped.Actual != conversations.Stopped {
		return Violatedf("a stopped session that never ran reads %+v", stopped)
	}
	sent, err := t.send(theMember, opened.Conversation.ID, "Are you there?")
	if err != nil {
		return err
	}
	if !sent.Conversation.Running() || sent.Conversation.Generation != 2 || sent.Message.State != messages.Queued {
		return Violatedf("a message to a stopped conversation left %+v and %+v", sent.Conversation, sent.Message)
	}
	if sent.Message.To.Of() != messages.TargetAssistant || sent.Message.To.Generation != sent.Conversation.Generation {
		return Violatedf("the message is addressed %+v", sent.Message.To)
	}
	retry, err := t.convs.Send(r.Ctx, r.Scope, opened.Conversation.ID, control.MemberMessage{ID: sent.Message.ID, Body: "Are you there?"}, theMember)
	if err != nil || retry.Entry.Seq != sent.Entry.Seq {
		return Violatedf("a retry recorded %+v (%v)", retry.Entry, err)
	}
	return nil
}

func checkAssistantCredential(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	delegates := control.NewDelegates(t.store)
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	record := opened.Conversation
	request := ledger.AssistantCredentialRequest{Auth: t.auth, Epoch: t.epoch, Conversation: record.ID, Lane: record.RunLane, Generation: 1}
	grant, err := t.plane.AssistantCredential(r.Ctx, request)
	if err != nil {
		return err
	}
	acts := func() error {
		credential, err := delegates.Authenticate(r.Ctx, r.Scope, grant.Token)
		if err != nil {
			return err
		}
		return control.AssistantActs(r.Ctx, t.store, r.Scope, credential)
	}
	if err := acts(); !errors.Is(err, authority.ErrBadDelegate) {
		return Violatedf("a credential acts before its session runs: %v", err)
	}
	if record, err = t.running(record); err != nil {
		return err
	}
	if err := acts(); err != nil {
		return Violatedf("a running generation's credential: %v", err)
	}
	listed, _, err := delegates.List(r.Ctx, r.Scope, theMember)
	if err != nil {
		return err
	}
	if len(listed) != 1 || listed[0].Kind != authority.DelegateAssistant || listed[0].Conversation != record.ID || listed[0].Generation != 1 || listed[0].ExpiresAt != 0 {
		return Violatedf("the credential reads %+v", listed)
	}
	if err := t.runUpload(record, ledger.UploadGenerationStarted, 2, nil); err != nil {
		return err
	}
	if err := acts(); !errors.Is(err, authority.ErrBadDelegate) {
		return Violatedf("an ended generation's credential: %v", err)
	}
	next, err := t.plane.AssistantCredential(r.Ctx, ledger.AssistantCredentialRequest{
		Auth: t.auth, Epoch: t.epoch, Conversation: record.ID, Lane: record.RunLane, Generation: 2,
	})
	if err != nil {
		return err
	}
	grant = next
	retried, err := t.plane.AssistantCredential(r.Ctx, ledger.AssistantCredentialRequest{
		Auth: t.auth, Epoch: t.epoch, Conversation: record.ID, Lane: record.RunLane, Generation: 2,
	})
	if err != nil {
		return err
	}
	if err := acts(); !errors.Is(err, authority.ErrBadDelegate) {
		return Violatedf("a generation's earlier credential acts after a retry made another: %v", err)
	}
	grant = retried
	if err := acts(); err != nil {
		return Violatedf("the next generation's credential: %v", err)
	}
	if _, err := t.plane.AssistantCredential(r.Ctx, request); err == nil {
		return Violatedf("a credential was made for a generation that ended")
	}
	if _, err := t.convs.Stop(r.Ctx, r.Scope, record.ID, theMember); err != nil {
		return err
	}
	if err := acts(); !errors.Is(err, authority.ErrBadDelegate) {
		return Violatedf("a stopped conversation's credential: %v", err)
	}
	err = t.store.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
		rows, err := tx.Members().ConversationDelegates(r.Scope, theMember.ID, record.ID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.RevokedAt == 0 {
				return Violatedf("credential %s of generation %d was never revoked", row.ID, row.Generation)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	opened, err = t.create(theMember, "")
	if err != nil {
		return err
	}
	record, err = t.running(opened.Conversation)
	if err != nil {
		return err
	}
	if grant, err = t.plane.AssistantCredential(r.Ctx, ledger.AssistantCredentialRequest{
		Auth: t.auth, Epoch: t.epoch, Conversation: record.ID, Lane: record.RunLane, Generation: 1,
	}); err != nil {
		return err
	}
	if _, err := control.NewSessions(t.store, nil).EndAll(r.Ctx, r.Scope, theMember); err != nil {
		return err
	}
	if _, err := delegates.Authenticate(r.Ctx, r.Scope, grant.Token); !errors.Is(err, authority.ErrBadDelegate) {
		return Violatedf("signing out everywhere left the assistant's credential: %v", err)
	}
	return nil
}

func checkAssistantReplyOnce(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	record, err := t.running(opened.Conversation)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(60 * 1000)
	reply := ledger.AssistantEntryRequest{Auth: t.auth, Epoch: t.epoch, Conversation: record.ID, Lane: record.RunLane, ID: "msg_01", Body: "Two merges wait."}
	for range 2 {
		if err := t.plane.AssistantEntry(r.Ctx, reply); err != nil {
			return err
		}
	}
	rows, err := t.entries(record.ID)
	if err != nil {
		return err
	}
	replies := 0
	for _, row := range rows {
		if row.Kind == conversations.KindReply {
			replies++
		}
	}
	if replies != 1 {
		return Violatedf("%d replies for one vendor message", replies)
	}
	after, err := t.read(record.ID)
	if err != nil || after.LastActiveAt <= record.LastActiveAt {
		return Violatedf("a reply did not count as activity: %+v (%v)", after, err)
	}
	other := reply
	other.Lane = "another-run"
	if err := t.plane.AssistantEntry(r.Ctx, other); err == nil {
		return Violatedf("a run that is not the conversation's recorded a reply")
	}
	return nil
}

func checkAssentDecidesNothing(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	record, err := t.running(opened.Conversation)
	if err != nil {
		return err
	}
	grant, err := t.plane.AssistantCredential(r.Ctx, ledger.AssistantCredentialRequest{
		Auth: t.auth, Epoch: t.epoch, Conversation: record.ID, Lane: record.RunLane, Generation: 1,
	})
	if err != nil {
		return err
	}
	if before, err := t.send(theMember, record.ID, "yes"); err != nil || assentOf(before.Entry) {
		return Violatedf("a yes with no card read as assent (%v)", err)
	}
	approvals := control.NewApprovals(t.store)
	binding, err := control.BindingFor("work_order.cancel", "route", "/v1/work-orders/x/cancel", json.RawMessage(`{"reason":"r"}`))
	if err != nil {
		return err
	}
	digest, err := approvals.Bind(r.Ctx, r.Scope, binding)
	if err != nil {
		return err
	}
	card, err := approvals.Prepare(r.Ctx, r.Scope, authority.Approval{
		Subject: binding.Subject(), SubjectDigest: digest, Summary: "Cancel work: x", Detail: `{"operation":"work_order.cancel"}`,
	}, authority.DelegateActor(theMember.ID, grant.Credential))
	if err != nil {
		return err
	}
	sent, err := t.send(theMember, record.ID, "Yes go ahead!")
	if err != nil {
		return err
	}
	if !assentOf(sent.Entry) {
		return Violatedf("a short yes while a card waits is not marked: %s", sent.Entry.Data)
	}
	lane, err := t.send(theMember, record.ID, "approve")
	if err != nil {
		return err
	}
	if !assentOf(lane.Entry) {
		return Violatedf("approve while a card waits is not marked")
	}
	after, err := approvals.Get(r.Ctx, r.Scope, card.ID)
	if err != nil {
		return err
	}
	if after.State != authority.ApprovalPending || after.DecidedBy != "" {
		return Violatedf("a message decided the card: %+v", after)
	}
	return nil
}

func assentOf(entry conversations.Entry) bool {
	data, err := entry.Member()
	return err == nil && data.Assent
}

func checkAssistantRunStates(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	record := opened.Conversation
	steps := []struct {
		state string
		want  conversations.ActualState
	}{{"working", conversations.Working}, {"idle", conversations.Running}, {"compacting", conversations.Working}}
	for _, step := range steps {
		if err := t.runUpload(record, ledger.UploadLaneStatus, 1, ledger.LaneStatus{ActualState: step.state}); err != nil {
			return err
		}
		if got, err := t.read(record.ID); err != nil || got.Actual != step.want {
			return Violatedf("lane state %s reads %s (%v)", step.state, got.Actual, err)
		}
	}
	if err := t.runUpload(record, ledger.UploadLaneEnded, 1, map[string]any{"outcome": "failed"}); err != nil {
		return err
	}
	ended, err := t.read(record.ID)
	if err != nil {
		return err
	}
	if ended.Running() || ended.Actual != conversations.Stopped || ended.StoppedReason != conversations.ReasonSessionEnd {
		return Violatedf("a run that ended leaves %+v", ended)
	}
	failed, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	if err := t.runUpload(failed.Conversation, ledger.UploadLaneEnded, 1, map[string]any{"outcome": "failed"}); err != nil {
		return err
	}
	never, err := t.read(failed.Conversation.ID)
	if err != nil || never.Actual != conversations.Failed || never.StoppedReason != conversations.ReasonFailed {
		return Violatedf("a run that never started reads %+v (%v)", never, err)
	}
	return nil
}

func checkAssistantDesired(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	sent, err := t.send(theMember, opened.Conversation.ID, "Hello")
	if err != nil {
		return err
	}
	state, err := t.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: t.auth})
	if err != nil {
		return err
	}
	if len(state.Assistants) != 1 {
		return Violatedf("desired state names %d assistants", len(state.Assistants))
	}
	desired := state.Assistants[0]
	if desired.Conversation != opened.Conversation.ID || desired.Lane != opened.Conversation.RunLane || desired.Generation != 1 ||
		desired.Member != theMember.ID || desired.IdleStopMs != 15*60*1000 || desired.Fresh {
		return Violatedf("the assistant reads %+v", desired)
	}
	if len(desired.Messages) != 1 || desired.Messages[0].ID != sent.Message.ID || desired.Messages[0].Body != "Hello" ||
		desired.Messages[0].Lane != opened.Conversation.RunLane || desired.Messages[0].Generation != 1 {
		return Violatedf("its messages read %+v", desired.Messages)
	}
	if err := t.runUpload(opened.Conversation, ledger.UploadMessage, 1, messages.Report{ID: sent.Message.ID, State: messages.AtWorker}); err != nil {
		return err
	}
	if _, err := t.running(opened.Conversation); err != nil {
		return err
	}
	if _, err := t.convs.Compact(r.Ctx, r.Scope, opened.Conversation.ID, theMember); err != nil {
		return err
	}
	cleared, err := t.convs.Clear(r.Ctx, r.Scope, opened.Conversation.ID, theMember)
	if err != nil {
		return err
	}
	if state, err = t.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: t.auth}); err != nil {
		return err
	}
	desired = state.Assistants[0]
	if len(desired.Messages) != 0 || desired.Lane != cleared.RunLane || !desired.Fresh || desired.Generation != 2 || desired.Compactions != 0 {
		return Violatedf("after a clear the assistant reads %+v", desired)
	}
	other, err := t.register("basalt")
	if err != nil {
		return err
	}
	if state, err = t.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: other}); err != nil || len(state.Assistants) != 0 {
		return Violatedf("another worker is told to run %+v (%v)", state.Assistants, err)
	}
	return nil
}

func checkAssistantPlacement(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	create := func(worker string) error {
		_, err := t.convs.Create(r.Ctx, r.Scope, control.NewConversation{ID: r.ID("conv"), Worker: worker}, theMember)
		return err
	}
	if err := create(""); err != nil {
		return Violatedf("the only worker that can run it: %v", err)
	}
	basalt, err := t.register("basalt")
	if err != nil {
		return err
	}
	t.hosts[basalt.WorkerID] = control.AssistantHost{Headless: true, Checkouts: map[string]bool{t.repo: true}}
	err = create("")
	if !errors.Is(err, conversations.ErrInvalid) || err.Error() != "Choose a worker: atlas and basalt can run the assistant." {
		return Violatedf("two that can, none asked for: %v", err)
	}
	if err := create("basalt"); err != nil {
		return Violatedf("a worker asked for by name: %v", err)
	}
	t.hosts[basalt.WorkerID] = control.AssistantHost{Headless: true}
	t.hosts[t.auth.WorkerID] = control.AssistantHost{Checkouts: map[string]bool{t.repo: true}}
	cedar, err := t.register("cedar")
	if err != nil {
		return err
	}
	t.hosts[cedar.WorkerID] = control.AssistantHost{Headless: true, Checkouts: map[string]bool{t.repo: true}}
	r.DB.ShiftClock(10 * statusHeartbeatMs)
	err = create("")
	want := "No worker can run the assistant: atlas is disconnected; basalt is disconnected; cedar is disconnected."
	if !errors.Is(err, conversations.ErrNoWorker) || err.Error() != want {
		return Violatedf("none connected: %v", err)
	}
	for _, auth := range []ledger.WorkerAuth{t.auth, basalt, cedar} {
		if _, err := t.plane.OpenSession(r.Ctx, ledger.SessionRequest{Auth: auth, Protocol: ledger.ProtocolVersion, Manifest: UsableManifest}); err != nil {
			return err
		}
	}
	delete(t.hosts, cedar.WorkerID)
	err = create("")
	want = "No worker can run the assistant: atlas runs no headless session; basalt has no checkout of harbor-api; cedar runs no headless session."
	if !errors.Is(err, conversations.ErrNoWorker) || err.Error() != want {
		return Violatedf("none able: %v", err)
	}
	workers, err := t.convs.Workers(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(workers) != 3 || workers[1].Name != "basalt" || workers[1].Available || workers[1].Reason != "has no checkout of harbor-api" {
		return Violatedf("workers read %+v", workers)
	}
	if err := create("basalt"); !errors.Is(err, conversations.ErrNoWorker) {
		return Violatedf("a worker that cannot, asked for by name: %v", err)
	}
	return nil
}

func checkOrchestratorMessages(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	ops := control.NewMessages(t.store, nil).Hosting(t.hostsOf)
	send := func(worker, body string) (messages.Message, error) {
		return ops.Send(r.Ctx, r.Scope, messages.Draft{
			ID: r.ID("m"), To: messages.To(messages.TargetOrchestrator, worker, 0), Kind: messages.KindInstruction, Body: body,
		}, theMember)
	}
	_, err = send(t.auth.WorkerID, "Look at WO-1")
	if !errors.Is(err, messages.ErrNotRunning) || err.Error() != "The orchestrator on atlas is not running. Start it from Workers." {
		return Violatedf("a stopped orchestrator: %v", err)
	}
	if _, err := t.plane.SetOrchestrator(r.Ctx, t.auth.WorkerID, ledger.OrchestratorRunning, theMember); err != nil {
		return err
	}
	stranger, err := t.register("basalt")
	if err != nil {
		return err
	}
	if _, err := t.plane.SetOrchestrator(r.Ctx, stranger.WorkerID, ledger.OrchestratorRunning, theMember); err != nil {
		return err
	}
	if _, err := send(stranger.WorkerID, "Hello"); !errors.Is(err, messages.ErrInvalid) {
		return Violatedf("an orchestrator that serves no repository of the project: %v", err)
	}
	if _, err := ops.Send(r.Ctx, r.Scope, messages.Draft{
		ID: r.ID("m"), To: messages.To(messages.TargetAssistant, "c", 0), Kind: messages.KindInstruction, Body: "x",
	}, theMember); !errors.Is(err, messages.ErrInvalid) || !strings.Contains(err.Error(), "goes through its conversation") {
		return Violatedf("a message to the assistant on the general path: %v", err)
	}
	sent, err := send(t.auth.WorkerID, "Look at WO-1 first")
	if err != nil {
		return err
	}
	digest, err := t.plane.WakeDigest(r.Ctx, ledger.WakeRequest{Auth: t.auth, Epoch: t.epoch})
	if err != nil {
		return err
	}
	var item *ledger.WakeItem
	for i := range digest.Items {
		if digest.Items[i].Kind == ledger.WakeMessage {
			item = &digest.Items[i]
		}
	}
	if item == nil || item.ID != sent.ID || !strings.Contains(item.Text, "Look at WO-1 first") || !strings.Contains(item.Text, "old") {
		return Violatedf("the digest holds %+v", digest.Items)
	}
	report := func(state messages.State, reply string) error {
		raw, err := json.Marshal(messages.Report{ID: sent.ID, State: state, Reply: reply})
		if err != nil {
			return err
		}
		t.seq++
		answer, err := t.plane.Upload(r.Ctx, ledger.UploadBatch{Auth: t.auth, Epoch: t.epoch, Events: []ledger.UploadEvent{{
			Epoch: t.epoch, Seq: t.seq, Type: ledger.UploadMessage, Payload: raw, OccurredAt: 1,
		}}})
		if err == nil && len(answer.Rejected) != 0 {
			return fmt.Errorf("refused: %+v", answer.Rejected)
		}
		return err
	}
	for _, step := range []struct {
		state messages.State
		reply string
	}{{messages.AtWorker, ""}, {messages.HandedOff, ""}, {messages.Replied, "On it."}} {
		if err := report(step.state, step.reply); err != nil {
			return err
		}
		read, err := ops.ReadMessage(r.Ctx, r.Scope, sent.ID)
		if err != nil {
			return err
		}
		if read.State != step.state {
			return Violatedf("after %s the message reads %s", step.state, read.State)
		}
	}
	if digest, err = t.plane.WakeDigest(r.Ctx, ledger.WakeRequest{Auth: t.auth, Epoch: t.epoch}); err != nil {
		return err
	}
	for _, item := range digest.Items {
		if item.Kind == ledger.WakeMessage {
			return Violatedf("a replied message still wakes the orchestrator")
		}
	}
	read, err := ops.ReadMessage(r.Ctx, r.Scope, sent.ID)
	if err != nil || read.Reply != "On it." {
		return Violatedf("the reply reads %q (%v)", read.Reply, err)
	}
	other, err := send(t.auth.WorkerID, "Another one")
	if err != nil {
		return err
	}
	raw, err := json.Marshal(messages.Report{ID: other.ID, State: messages.AtWorker})
	if err != nil {
		return err
	}
	epoch := t.epochs[stranger.WorkerID]
	answer, err := t.plane.Upload(r.Ctx, ledger.UploadBatch{Auth: stranger, Epoch: epoch, Events: []ledger.UploadEvent{{
		Epoch: epoch, Seq: 1, Type: ledger.UploadMessage, Payload: raw, OccurredAt: 1,
	}}})
	if err == nil && len(answer.Rejected) == 0 {
		return Violatedf("another worker spoke for a message to this worker's orchestrator")
	}
	return nil
}

func checkAssistantCredentialEnd(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	credentialOf := func(record conversations.Conversation) (authority.DelegateCredential, error) {
		if _, err := t.plane.AssistantCredential(r.Ctx, ledger.AssistantCredentialRequest{
			Auth: t.auth, Epoch: t.epoch, Conversation: record.ID, Lane: record.RunLane, Generation: 1,
		}); err != nil {
			return authority.DelegateCredential{}, err
		}
		var credential authority.DelegateCredential
		err := t.store.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
			rows, err := tx.Members().ConversationDelegates(r.Scope, theMember.ID, record.ID)
			if err == nil && len(rows) == 1 {
				credential = rows[0]
			}
			return err
		})
		return credential, err
	}
	read := func(id string) (authority.DelegateCredential, error) {
		var credential authority.DelegateCredential
		err := t.store.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
			var err error
			credential, err = tx.Members().Delegate(id)
			return err
		})
		return credential, err
	}
	revoke := func(id, reason string) (bool, error) {
		var changed bool
		err := t.store.Installation(r.Scope).Write(r.Ctx, "delegate.revoke", func(tx ports.SharedTx) error {
			var err error
			changed, err = tx.Members().RevokeDelegate(id, tx.Now()+1, reason)
			return err
		})
		return changed, err
	}
	stoppedCredential := func() (authority.DelegateCredential, error) {
		opened, err := t.create(theMember, "")
		if err != nil {
			return authority.DelegateCredential{}, err
		}
		record, err := t.running(opened.Conversation)
		if err != nil {
			return authority.DelegateCredential{}, err
		}
		credential, err := credentialOf(record)
		if err != nil {
			return authority.DelegateCredential{}, err
		}
		if _, err := t.convs.Stop(r.Ctx, r.Scope, record.ID, theMember); err != nil {
			return authority.DelegateCredential{}, err
		}
		return read(credential.ID)
	}
	revoked, err := stoppedCredential()
	if err != nil {
		return err
	}
	if revoked.RevokedAt == 0 || revoked.EndedReason != authority.DelegateEndStopped || !revoked.EndedRoutinely() {
		return Violatedf("a stopped conversation's credential reads %+v", revoked)
	}
	if changed, err := revoke(revoked.ID, authority.DelegateEndGeneration); err != nil || changed {
		return Violatedf("a routine end overwrote another: %v %v", changed, err)
	}
	if _, err := control.NewDelegates(t.store).Revoke(r.Ctx, r.Scope, revoked.ID, theMember); err != nil {
		return err
	}
	after, err := read(revoked.ID)
	if err != nil {
		return err
	}
	if after.EndedReason != authority.DelegateEndRevoked || after.RevokedAt != revoked.RevokedAt || after.EndedRoutinely() {
		return Violatedf("a member's revocation of a routine end reads %+v", after)
	}
	for _, reason := range []string{authority.DelegateEndStopped, authority.DelegateEndSignedOut} {
		if changed, err := revoke(revoked.ID, reason); err != nil || changed {
			return Violatedf("%s overwrote a member's revocation: %v %v", reason, changed, err)
		}
	}
	signedOut, err := stoppedCredential()
	if err != nil {
		return err
	}
	ended, err := control.NewSessions(t.store, nil).EndAll(r.Ctx, r.Scope, theMember)
	if err != nil {
		return err
	}
	if after, err = read(signedOut.ID); err != nil {
		return err
	}
	if after.EndedReason != authority.DelegateEndSignedOut || !slices.Contains(ended.Ended, signedOut.ID) || slices.Contains(ended.Delegates, signedOut.ID) {
		return Violatedf("signing out everywhere left %+v, ending %+v", after, ended)
	}
	return nil
}

func checkConversationIDScoped(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	record, err := t.running(opened.Conversation)
	if err != nil {
		return err
	}
	if _, err := t.plane.AssistantCredential(r.Ctx, ledger.AssistantCredentialRequest{
		Auth: t.auth, Epoch: t.epoch, Conversation: record.ID, Lane: record.RunLane, Generation: 1,
	}); err != nil {
		return err
	}
	// Credentials under the same conversation ID that another project, or another member, holds.
	foreign := []authority.DelegateCredential{
		{ID: r.ID("dlg"), Digest: r.ID("digest"), Member: theMember.ID, Project: ledger.WorkspaceID(r.ID("other-project")), Label: authority.AssistantLabel},
		{ID: r.ID("dlg"), Digest: r.ID("digest"), Member: theOtherMember.ID, Project: r.Scope, Label: authority.AssistantLabel},
	}
	err = t.store.Installation(r.Scope).Write(r.Ctx, "delegate.assistant", func(tx ports.SharedTx) error {
		for _, credential := range foreign {
			credential.Kind, credential.CreatedAt, credential.Conversation, credential.Generation = authority.DelegateAssistant, tx.Now(), record.ID, 1
			if err := tx.Members().InsertDelegate(credential); err != nil {
				return err
			}
			if _, err := tx.Append(ledger.EventDraft{
				Type: control.EventDelegateCreated, Actor: theMember, Subject: ledger.Subject{Kind: "delegate", ID: credential.ID}, Data: []byte(`{}`),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	var listed []authority.DelegateCredential
	err = t.store.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
		var err error
		listed, err = tx.Members().ConversationDelegates(r.Scope, theMember.ID, record.ID)
		return err
	})
	if err != nil {
		return err
	}
	if len(listed) != 1 || listed[0].Project != r.Scope || listed[0].Member != theMember.ID {
		return Violatedf("the conversation's credentials read %+v", listed)
	}
	if err := t.convs.Delete(r.Ctx, r.Scope, record.ID, theMember); err != nil {
		return err
	}
	err = t.store.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
		for _, credential := range foreign {
			row, err := tx.Members().Delegate(credential.ID)
			if err != nil {
				return err
			}
			if row.RevokedAt != 0 {
				return Violatedf("deleting a conversation revoked %s's credential in %s", row.Member, row.Project)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, actor := range []ledger.Actor{theMember, theOtherMember} {
		if _, err := t.convs.Create(r.Ctx, r.Scope, control.NewConversation{ID: record.ID}, actor); !errors.Is(err, conversations.ErrInvalid) {
			return Violatedf("a deleted conversation's ID was taken again by %s: %v", actor.ID, err)
		}
	}
	return nil
}
