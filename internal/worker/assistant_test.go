package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
)

// assistantRig is an orchestrator rig whose worker can run a member's assistant: it runs headless sessions
// and holds every repository of the project, which has none.
type assistantRig struct {
	*orchestratorRig
	convs *control.Conversations
}

func newAssistantRig(t *testing.T, scripts ...fake.DriverScript) *assistantRig {
	t.Helper()
	o := newOrchestratorRig(t, scripts...)
	o.config.AssistantCommands, o.config.Routes = []string{"lane list", "work show"}, []string{"lane-shim", "hook"}
	o.tick(t)
	a := &assistantRig{orchestratorRig: o}
	a.convs = control.NewConversations(o.store, nil).Hosting(func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
		return map[string]control.AssistantHost{o.subject.Auth().WorkerID: {Headless: true}}, nil
	}, 0)
	return a
}

// assistantSpecs lists the assistant's sessions.
func (a *assistantRig) assistantSpecs() []ports.SessionSpec {
	var out []ports.SessionSpec
	for _, spec := range a.driver.Specs() {
		if spec.Assistant {
			out = append(out, spec)
		}
	}
	return out
}

func (a *assistantRig) awaitAssistant(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		a.tick(t)
		if err := a.subject.Publish(a.ctx); err != nil {
			t.Fatal(err)
		}
		if err := a.subject.Upload(a.ctx); err != nil {
			t.Fatal(err)
		}
		if done() {
			return
		}
	}
	t.Fatalf("%s never happened", what)
}

func (a *assistantRig) conversation(t *testing.T, id string) conversations.Conversation {
	t.Helper()
	record, err := a.convs.Get(t.Context(), scope, id, member)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func (a *assistantRig) replies(t *testing.T, id string) []string {
	t.Helper()
	page, err := a.convs.Entries(t.Context(), scope, id, 0, 500, member)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range page.Entries {
		if entry.Kind == conversations.KindReply {
			out = append(out, entry.Body)
		}
	}
	return out
}

func envOf(spec ports.SessionSpec, name string) string {
	for _, entry := range spec.Env {
		if value, ok := strings.CutPrefix(entry, name+"="); ok {
			return value
		}
	}
	return ""
}

// An assistant runs as a session with its own credential, which reaches only its process's environment,
// reads its member's messages through the injection path, and records each reply it writes once.
func TestAnAssistantRunsWithItsOwnCredentialAndRepliesOnce(t *testing.T) {
	reply := []fake.DriverStep{{Reply: "Two merges wait.", ReplyID: "msg_01"}, {Reply: "Two merges wait.", ReplyID: "msg_01"}, completed(0.01)}
	a := newAssistantRig(t, script([]fake.DriverStep{{Bound: true}, {Reply: "Ready.", ReplyID: "msg_00"}, completed(0.01)}, reply, []fake.DriverStep{{Hold: true}}))
	ctx := t.Context()
	opened, err := a.convs.Create(ctx, scope, control.NewConversation{ID: "conv-1"}, member)
	if err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "an assistant's session", func() bool { return len(a.assistantSpecs()) == 1 })
	spec := a.assistantSpecs()[0]
	token := envOf(spec, authority.EnvDelegateToken)
	if token == "" || envOf(spec, "ROPESHIFT_PROJECT") == "" || !spec.ReadOnly || spec.OnlyTools || spec.RunMode != ports.RunModeHeadless {
		t.Fatalf("the session's spec %+v", spec)
	}
	if strings.Join(spec.Tools, ",") != strings.Join([]string{lanes.ToolContextGet, lanes.ToolDecisionSearch, lanes.ToolRequirementGet, lanes.ToolQuestionSearch}, ",") {
		t.Errorf("tools %v", spec.Tools)
	}
	if strings.Join(spec.Commands, ",") != "lane list,work show" || strings.Join(spec.Routes, ",") != "lane-shim,hook" {
		t.Errorf("the shell may run %v and its rules deny %v", spec.Commands, spec.Routes)
	}
	credential, err := control.NewDelegates(a.store).Authenticate(ctx, scope, token)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Kind != authority.DelegateAssistant || credential.Conversation != "conv-1" || credential.Generation != 1 || credential.Member != member.ID {
		t.Fatalf("the credential is %+v", credential)
	}
	a.awaitAssistant(t, "a session at rest", func() bool { return a.conversation(t, "conv-1").Actual == conversations.Running })
	if _, err := a.convs.Send(ctx, scope, "conv-1", control.MemberMessage{ID: "m-1", Body: "Which merges wait?"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "a reply", func() bool { return len(a.replies(t, "conv-1")) == 1 })
	for range 3 {
		a.tick(t)
	}
	if got := a.replies(t, "conv-1"); len(got) != 1 || got[0] != "Two merges wait." {
		t.Fatalf("replies %q", got)
	}
	message, err := control.NewMessages(a.store, nil).ReadMessage(ctx, scope, "m-1")
	if err != nil || message.State == messages.Queued {
		t.Fatalf("the member's message stands %s (%v)", message.State, err)
	}
	scanned, err := json.Marshal(journalOf(t, a.orchestratorRig))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(scanned), token) {
		t.Fatal("the journal holds the assistant's credential")
	}
	if _, err := a.convs.Stop(ctx, scope, opened.Conversation.ID, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "the session's end", func() bool { return a.conversation(t, "conv-1").Actual == conversations.Stopped })
	if _, err := control.NewDelegates(a.store).Authenticate(ctx, scope, token); err == nil {
		t.Error("a stopped conversation's credential still acts")
	}
}

// journalOf reads what a worker's journal holds that could carry a secret.
func journalOf(t *testing.T, o *orchestratorRig) map[string]any {
	t.Helper()
	ctx := t.Context()
	records, err := o.journal.Lanes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := o.journal.Pending(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := o.journal.Desired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := o.journal.Effects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"lanes": records, "pending": pending, "desired": desired, "effects": effects}
}

// An assistant never counts toward a lane cap, and admission never holds it (FR-PROJ-7).
func TestAnAssistantTakesNoLanesPlace(t *testing.T) {
	a := newAssistantRig(t, script([]fake.DriverStep{{Bound: true}, completed(0.01)}))
	a.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser(orchestratorProfile + "[admission]\nmax_lanes = 2\nmax_lanes_per_sign_in = 2\n")
	if _, err := a.convs.Create(t.Context(), scope, control.NewConversation{ID: "conv-1"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "an assistant's session", func() bool { return len(a.assistantSpecs()) == 1 })
	a.secondLane("wo-2", "lane-2")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		a.tick(t)
		for _, spec := range a.driver.Specs() {
			if spec.Lane == "lane-2" {
				return
			}
		}
	}
	t.Error("a second lane under its cap never started")
}

// Every call no rule allows an assistant is denied by the hook, with a reason, and never raised as a
// request a member would have to answer.
func TestAnAssistantsDeniedCallRaisesNoRequest(t *testing.T) {
	a := newAssistantRig(t, script([]fake.DriverStep{{Bound: true}, completed(0.01)}))
	if _, err := a.convs.Create(t.Context(), scope, control.NewConversation{ID: "conv-1"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "an assistant's session", func() bool { return len(a.assistantSpecs()) == 1 })
	token := tokenIn(a.assistantSpecs()[0])
	for _, request := range []lanes.Request{
		{Version: lanes.SocketProtocol, Token: token, Approval: &lanes.ApprovalRequest{Tool: "Edit", Input: json.RawMessage(`{"file_path":"/x"}`), Call: "c1"}},
		{Version: lanes.SocketProtocol, Token: token, Approval: &lanes.ApprovalRequest{Tool: "Bash", Input: json.RawMessage(`{"command":"rm -rf /"}`), Call: "c2"}},
	} {
		line, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		reply := a.subject.AnswerLane(t.Context(), line)
		if reply.Approval == nil || reply.Approval.Decision != lanes.ApprovalDeny || !strings.Contains(reply.Approval.Reason, "ropeshift") || reply.Approval.ID != "" {
			t.Errorf("%s: %+v", request.Approval.Tool, reply)
		}
	}
	for _, request := range []lanes.Request{
		{Version: lanes.SocketProtocol, Token: token, Ask: &lanes.AskRequest{Text: "Which?"}},
		{Version: lanes.SocketProtocol, Token: token, Work: &lanes.WorkRequest{Tool: lanes.ToolWorkCandidates}},
	} {
		line, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if reply := a.subject.AnswerLane(t.Context(), line); reply.OK {
			t.Errorf("an assistant's token did %+v", request)
		}
	}
	pending, err := a.journal.Pending(t.Context(), 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range pending {
		if event.Type == ledger.UploadApprovalRaised {
			t.Fatalf("a denied call raised a request: %s", event.Payload)
		}
	}
	approvals, err := control.NewApprovals(a.store).List(t.Context(), scope, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals) != 0 {
		t.Fatalf("requests %+v", approvals)
	}
}

// A member's message to the orchestrator reaches it as one item of its wake, is held by its worker, handed
// off when the wake reaches a turn, and replied to through the reply path a lane uses.
func TestAMessageToTheOrchestratorWalksItsStates(t *testing.T) {
	o := newOrchestratorRig(t, aSession([]fake.DriverStep{completed(0.01)}))
	ctx := t.Context()
	repository, err := control.NewRepositories(o.store).Detect(ctx, scope, control.DetectedRepository{ID: "repo-1", Directory: "harbor-api"}, member)
	if err != nil {
		t.Fatal(err)
	}
	o.start(t)
	o.tick(t)
	sending := control.NewMessages(o.store, nil).Hosting(func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
		return map[string]control.AssistantHost{o.subject.Auth().WorkerID: {Checkouts: map[string]bool{repository.ID: true}}}, nil
	})
	sent, err := sending.Send(ctx, scope, messages.Draft{
		ID: "m-orch", To: messages.To(messages.TargetOrchestrator, o.subject.Auth().WorkerID, 0), Kind: messages.KindInstruction, Body: "Look at WO-1",
	}, member)
	if err != nil {
		t.Fatal(err)
	}
	stateOf := func() messages.State {
		row, err := sending.ReadMessage(ctx, scope, sent.ID)
		if err != nil {
			t.Fatal(err)
		}
		return row.State
	}
	o.awaitNoted(t, 1)
	o.clock.Advance(31 * time.Second)
	o.awaitSessions(t, 1)
	if brief := o.specs()[0].Brief; !strings.Contains(brief, "Look at WO-1") {
		t.Fatalf("the wake holds no message: %q", brief)
	}
	o.awaitRest(t)
	o.await(t, "a hand-off", func(ledger.Orchestrator) bool { return stateOf() == messages.HandedOff })
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: tokenIn(o.specs()[0]), Reply: &lanes.ReplyRequest{ReplyTo: sent.ID, Body: "On it."}})
	if err != nil {
		t.Fatal(err)
	}
	if reply := o.subject.AnswerLane(ctx, line); !reply.OK {
		t.Fatalf("the orchestrator's reply: %+v", reply)
	}
	o.await(t, "a reply", func(ledger.Orchestrator) bool { return stateOf() == messages.Replied })
	row, err := sending.ReadMessage(ctx, scope, sent.ID)
	if err != nil || row.Reply != "On it." {
		t.Fatalf("the reply reads %+v (%v)", row, err)
	}
}

// Whatever the member's profile runs lanes in, an assistant's session runs in the mode that asks, so a
// call no rule allows reaches the hook, which denies it.
func TestAnAssistantRunsInTheModeThatAsks(t *testing.T) {
	a := newAssistantRig(t, script([]fake.DriverStep{{Bound: true}, completed(0.01)}))
	a.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser(orchestratorProfile + "[agent]\npermission_mode = \"auto\"\n")
	if _, err := a.convs.Create(t.Context(), scope, control.NewConversation{ID: "conv-1"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "an assistant's session", func() bool { return len(a.assistantSpecs()) == 1 })
	if mode := a.assistantSpecs()[0].PermissionMode; mode != "default" {
		t.Fatalf("an assistant runs in %q", mode)
	}
}

// A conversation's session runs in one directory at every start, so a later start can resume it where the
// vendor keeps it. A resume the vendor refuses starts over: a fresh session whose first prompt holds the
// conversation so far, and the refused session is not tried again.
func TestAnAssistantResumesInItsOwnDirectoryAndStartsOverWhenRefused(t *testing.T) {
	first := fake.DriverScript{SessionID: "session-a", Model: "resolved-model", Turns: [][]fake.DriverStep{
		{{Bound: true}, completed(0.01)}, {{Reply: "Two merges wait.", ReplyID: "msg_01"}, completed(0.01)}, {{Hold: true}},
	}}
	refused := fake.DriverScript{SessionID: "session-x", Turns: [][]fake.DriverStep{{{Crash: true}}}}
	fresh := fake.DriverScript{SessionID: "session-b", Model: "resolved-model", Turns: [][]fake.DriverStep{{{Bound: true}, completed(0.01)}, {{Hold: true}}}}
	later := fake.DriverScript{SessionID: "session-b", Model: "resolved-model", Turns: [][]fake.DriverStep{{{Bound: true}, completed(0.01)}, {{Hold: true}}}}
	a := newAssistantRig(t, first, refused, fresh, later)
	ctx := t.Context()
	if _, err := a.convs.Create(ctx, scope, control.NewConversation{ID: "conv-1"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "a session at rest", func() bool { return a.conversation(t, "conv-1").Actual == conversations.Running })
	if _, err := a.convs.Send(ctx, scope, "conv-1", control.MemberMessage{ID: "m-1", Body: "Which merges wait?"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "a reply", func() bool { return len(a.replies(t, "conv-1")) == 1 })
	reopen := func() {
		t.Helper()
		if _, err := a.convs.Stop(ctx, scope, "conv-1", member); err != nil {
			t.Fatal(err)
		}
		a.awaitAssistant(t, "the session's end", func() bool { return a.conversation(t, "conv-1").Actual == conversations.Stopped })
		if _, err := a.convs.Open(ctx, scope, "conv-1", "", member); err != nil {
			t.Fatal(err)
		}
	}
	reopen()
	a.awaitAssistant(t, "a fresh session after the refusal", func() bool { return len(a.assistantSpecs()) == 3 })
	specs := a.assistantSpecs()
	if specs[0].Dir == "" || specs[1].Dir != specs[0].Dir || specs[2].Dir != specs[0].Dir {
		t.Errorf("the sessions ran in %q, %q, and %q", specs[0].Dir, specs[1].Dir, specs[2].Dir)
	}
	if resumed := a.driver.Resumed(); len(resumed) != 1 || resumed[0].SessionID != "session-a" {
		t.Fatalf("resumed %+v", resumed)
	}
	if !strings.Contains(specs[2].Brief, "Which merges wait?") {
		t.Errorf("the fresh session's first prompt holds no history: %q", specs[2].Brief)
	}
	a.awaitAssistant(t, "the fresh session at rest", func() bool { return a.conversation(t, "conv-1").Actual == conversations.Running })
	reopen()
	a.awaitAssistant(t, "a later start", func() bool { return len(a.driver.Resumed()) == 2 })
	if resumed := a.driver.Resumed(); resumed[1].SessionID != "session-b" {
		t.Errorf("a later start resumed %+v", resumed)
	}
}

// Deleting a conversation removes what its worker holds of it, once its session stopped: the messages to
// it and its runs' records in the journal, and the directory its sessions ran in with the session it kept.
// Nothing anyone wrote in it stays in the journal, its cached desired state included, and the control plane
// hears that the worker forgot it.
func TestDeletingAConversationLeavesNothingOfItOnTheWorker(t *testing.T) {
	reply := []fake.DriverStep{{Reply: "Two merges wait.", ReplyID: "msg_01"}, completed(0.01)}
	a := newAssistantRig(t, script([]fake.DriverStep{{Bound: true}, completed(0.01)}, reply, []fake.DriverStep{{Hold: true}}))
	ctx := t.Context()
	if _, err := a.convs.Create(ctx, scope, control.NewConversation{ID: "conv-1"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "a session at rest", func() bool { return a.conversation(t, "conv-1").Actual == conversations.Running })
	if _, err := a.convs.Send(ctx, scope, "conv-1", control.MemberMessage{ID: "m-1", Body: "The launch codes are 1234"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "a reply", func() bool { return len(a.replies(t, "conv-1")) == 1 })
	run := a.assistantSpecs()[0].Lane
	home := filepath.Join(a.dir, "assistants", "conv-1")
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("the conversation's directory: %v", err)
	}
	if err := a.convs.Delete(ctx, scope, "conv-1", member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "the worker forgetting the conversation", func() bool {
		state, err := a.journal.Desired(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, gone := os.Stat(home)
		return len(state.Forget) == 0 && len(state.Assistants) == 0 && errors.Is(gone, os.ErrNotExist)
	})
	if _, err := a.journal.Message(ctx, "m-1"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the journal still holds the message: %v", err)
	}
	if _, err := a.journal.Lane(ctx, run); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the journal still holds the run's record: %v", err)
	}
	scanned, err := json.Marshal(journalOf(t, a.orchestratorRig))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(scanned), "launch codes") {
		t.Errorf("the journal holds what the member wrote: %s", scanned)
	}
}

// An assistant never runs in a pane, where its credential would sit in the pane's launch file: on a sign-in
// that offers no headless session its conversation cannot start, and says so, where a lane falls back to
// tmux.
func TestAnAssistantNeverFallsBackToAPane(t *testing.T) {
	a := newAssistantRig(t, script([]fake.DriverStep{{Bound: true}, completed(0.01)}))
	a.driver.SetHeadless(false)
	if _, err := a.convs.Create(t.Context(), scope, control.NewConversation{ID: "conv-1"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "a conversation that could not start", func() bool { return a.conversation(t, "conv-1").Actual == conversations.Failed })
	for _, spec := range a.driver.Specs() {
		if spec.Assistant {
			t.Errorf("an assistant's session started in %s", spec.RunMode)
		}
	}
}
