package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

var talkMember = ledger.Actor{Kind: ledger.ActorMember, ID: "member"}

// talkDaemon is a harness whose API knows where the assistant can run: one connected worker, atlas, that
// holds the project's one repository and runs headless sessions.
type talkDaemon struct {
	*apitest.Daemon
	socket  string
	client  *api.ProjectClient
	workers *control.Workers
	auth    ledger.WorkerAuth
	epoch   int64
	seq     int64
	repo    string
	hosts   map[string]control.AssistantHost
}

func startTalk(t *testing.T) *talkDaemon {
	t.Helper()
	d := &talkDaemon{Daemon: apitest.Start(t), hosts: map[string]control.AssistantHost{}}
	config := d.Config
	config.AssistantHosts = func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
		return d.hosts, nil
	}
	d.socket = filepath.Join(apitest.ShortDir(t), api.ControlSocketName)
	listener, err := net.Listen("unix", d.socket)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: server.New(config), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = httpServer.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = httpServer.Close()
		<-done
	})
	client := api.NewUnixClient(d.socket)
	t.Cleanup(client.Close)
	client.UseSession(d.Session)
	d.client = client.Project(apitest.ProjectName)
	d.workers = control.NewWorkers(d.Store, apitest.Scope, control.WorkerOptions{HeartbeatMs: apitest.HeartbeatMs, LeaseTTLMs: apitest.LeaseTTL})
	token, err := d.workers.CreateJoinToken(t.Context(), talkMember)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := d.workers.Register(t.Context(), ledger.Registration{JoinToken: token, WorkerID: "worker-atlas", Name: "atlas"})
	if err != nil {
		t.Fatal(err)
	}
	d.auth = ledger.WorkerAuth{WorkerID: "worker-atlas", Credential: credential.Credential}
	session, err := d.workers.OpenSession(t.Context(), ledger.SessionRequest{Auth: d.auth, Protocol: ledger.ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	d.epoch = session.Epoch
	repository, err := control.NewRepositories(d.Store).Detect(t.Context(), apitest.Scope, control.DetectedRepository{
		ID: "repo-harbor", Directory: "harbor-api", DefaultBranch: "main",
	}, talkMember)
	if err != nil {
		t.Fatal(err)
	}
	d.repo = repository.ID
	d.hosts[d.auth.WorkerID] = control.AssistantHost{Headless: true, Checkouts: map[string]bool{d.repo: true}}
	return d
}

// running reports a conversation's session at rest, as its worker would.
func (d *talkDaemon) running(t *testing.T, record conversations.Conversation) {
	t.Helper()
	raw, err := json.Marshal(ledger.LaneStatus{ActualState: "idle", Liveness: "alive"})
	if err != nil {
		t.Fatal(err)
	}
	d.seq++
	reply, err := d.workers.Upload(t.Context(), ledger.UploadBatch{Auth: d.auth, Epoch: d.epoch, Events: []ledger.UploadEvent{{
		Epoch: d.epoch, Seq: d.seq, Type: ledger.UploadLaneStatus, Lane: record.RunLane, Generation: 1, Payload: raw, OccurredAt: 1,
	}}})
	if err != nil || len(reply.Rejected) != 0 {
		t.Fatalf("upload: %+v %v", reply, err)
	}
}

// assistant opens a conversation, runs its session, and returns it with a client that holds the
// credential of its first generation.
func (d *talkDaemon) assistant(t *testing.T) (conversations.Conversation, *api.ProjectClient, ledger.AssistantGrant) {
	t.Helper()
	opened, err := d.client.CreateConversation(t.Context(), api.CreateConversation{ID: "conv-1"})
	if err != nil {
		t.Fatal(err)
	}
	record := opened.Conversation.Conversation
	d.running(t, record)
	grant, err := d.workers.AssistantCredential(t.Context(), ledger.AssistantCredentialRequest{
		Auth: d.auth, Epoch: d.epoch, Conversation: record.ID, Lane: record.RunLane, Generation: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := api.NewUnixClient(d.socket)
	t.Cleanup(client.Close)
	client.UseDelegate(grant.Token)
	client.UseWhy(aWhy)
	return record, client.Project(apitest.ProjectName), grant
}

func (d *talkDaemon) entries(t *testing.T, id string) []api.TalkEntry {
	t.Helper()
	page, err := d.client.ConversationEntries(t.Context(), id, api.TalkQuery{Limit: api.TalkMaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	return page.Entries
}

func TestAMembersConversationRunsThroughTheAPI(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	opened, err := d.client.CreateConversation(ctx, api.CreateConversation{ID: "conv-1"})
	if err != nil {
		t.Fatal(err)
	}
	if opened.Conversation.Desired != conversations.DesiredRunning || opened.Conversation.WorkerName != "atlas" || len(opened.Stopped) != 0 {
		t.Fatalf("opened %+v", opened)
	}
	list, err := d.client.Conversations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Conversations) != 1 || len(list.Workers) != 1 || !list.Workers[0].Available {
		t.Fatalf("list %+v", list)
	}
	sent, err := d.client.SendToAssistant(ctx, "conv-1", api.ConversationMessage{ID: "msg-1", Body: "Which merges wait for me?"})
	if err != nil {
		t.Fatal(err)
	}
	if sent.Entry.Kind != api.TalkKindMember || sent.Entry.Message == nil || sent.Entry.Message.State != messages.Queued || sent.Conversation.Name != "Which merges wait for me?" {
		t.Fatalf("sent %+v", sent)
	}
	entries := d.entries(t, "conv-1")
	if len(entries) != 2 || entries[0].Kind != api.TalkKindMarker || entries[0].Marker != conversations.MarkerStarted || entries[1].ID != "msg-1" {
		t.Fatalf("entries %+v", entries)
	}
	second, err := d.client.CreateConversation(ctx, api.CreateConversation{ID: "conv-2", Name: "Merges"})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Stopped) != 1 || second.Stopped[0].ID != "conv-1" {
		t.Fatalf("a second conversation stopped %+v", second.Stopped)
	}
	if renamed, err := d.client.RenameConversation(ctx, "conv-2", "Merge queue"); err != nil || renamed.Name != "Merge queue" {
		t.Fatalf("renamed %+v: %v", renamed, err)
	}
	if _, err := d.client.CompactConversation(ctx, "conv-1"); err == nil {
		t.Fatal("a stopped conversation compacted")
	} else {
		refusedWith(t, err, http.StatusConflict, api.CodeTargetNotRunning)
	}
	if err := d.client.DeleteConversation(ctx, "conv-1"); err != nil {
		t.Fatal(err)
	}
	_, err = d.client.Conversation(ctx, "conv-1")
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	page, err := d.client.ConversationEntries(ctx, "conv-2", api.TalkQuery{Limit: 1})
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("a page of one: %+v %v", page, err)
	}
	if _, err := d.client.ConversationEntries(ctx, "conv-2", api.TalkQuery{Limit: api.TalkMaxLimit + 1}); err == nil {
		t.Error("a page over the limit")
	}
}

func TestNoWorkerThatCanRunTheAssistantIsAConflict(t *testing.T) {
	d := startTalk(t)
	d.hosts[d.auth.WorkerID] = control.AssistantHost{Headless: true}
	_, err := d.client.CreateConversation(t.Context(), api.CreateConversation{ID: "conv-1"})
	refusedWith(t, err, http.StatusConflict, api.CodeNoAssistantWorker)
	var refused *api.Error
	if errors.As(err, &refused) && refused.Detail.Message != "No worker can run the assistant: atlas has no checkout of harbor-api." {
		t.Errorf("the refusal says %q", refused.Detail.Message)
	}
}

// An assistant's credential acts only while its conversation runs the credential's generation, and no
// conversation route takes it: those are the member's alone.
func TestAnAssistantCredentialActsOnlyWhileItsGenerationRuns(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	opened, err := d.client.CreateConversation(ctx, api.CreateConversation{ID: "conv-1"})
	if err != nil {
		t.Fatal(err)
	}
	record := opened.Conversation.Conversation
	grant, err := d.workers.AssistantCredential(ctx, ledger.AssistantCredentialRequest{
		Auth: d.auth, Epoch: d.epoch, Conversation: record.ID, Lane: record.RunLane, Generation: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := api.NewUnixClient(d.socket)
	defer client.Close()
	client.UseDelegate(grant.Token)
	assistant := client.Project(apitest.ProjectName)
	_, err = assistant.WorkOrders(ctx, "")
	refusedWith(t, err, http.StatusUnauthorized, api.CodeBadDelegate)
	d.running(t, record)
	if _, err := assistant.WorkOrders(ctx, ""); err != nil {
		t.Fatalf("a running generation's credential: %v", err)
	}
	if list, err := assistant.Conversations(ctx); err != nil || len(list.Conversations) != 1 {
		t.Fatalf("the assistant reads its member's conversations: %+v %v", list, err)
	}
	for _, call := range []func() error{
		func() error {
			_, err := assistant.CreateConversation(ctx, api.CreateConversation{ID: "conv-x"})
			return err
		},
		func() error { _, err := assistant.StopConversation(ctx, "conv-1"); return err },
		func() error { return assistant.DeleteConversation(ctx, "conv-1") },
		func() error {
			_, err := assistant.SendToAssistant(ctx, "conv-1", api.ConversationMessage{ID: "m-x", Body: "yes"})
			return err
		},
	} {
		refusedWith(t, call(), http.StatusForbidden, api.CodeMemberOnly)
	}
	delegates, err := d.client.Delegates(ctx)
	if err != nil || len(delegates.Delegates) != 0 {
		t.Fatalf("a member's list holds the assistant's credential: %+v %v", delegates, err)
	}
	if _, err := d.client.StopConversation(ctx, "conv-1"); err != nil {
		t.Fatal(err)
	}
	_, err = assistant.WorkOrders(ctx, "")
	refusedWith(t, err, http.StatusUnauthorized, api.CodeBadDelegate)
}

// What the assistant ran, and each card it prepared, is an entry of its conversation; a refusal is none. A
// start of a planning lane names the lane, so the conversation can follow it.
func TestTheAssistantsCallsAreEntriesOfItsConversation(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	record, assistant, _ := d.assistant(t)
	created, err := assistant.CreateWorkOrder(ctx, api.CreateWorkOrder{
		ID: "wo-plan", Kind: workorders.KindPlan, Title: "Plan the harbor", Criteria: []planning.Criterion{{ID: "c1", Text: "A plan"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assistant.CancelWork(ctx, created.ID, api.CancelWork{Reason: "Not now"}); err == nil {
		t.Fatal("a consent call ran")
	} else {
		preparedCard(t, err)
	}
	_, err = assistant.CreateConversation(ctx, api.CreateConversation{ID: "conv-x"})
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	if _, err := d.Client.Review(ctx, "wo-plan"); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewLeases(d.Store).Claim(ctx, apitest.Scope, workorders.ClaimRequest{
		WorkOrder: "wo-plan", ClaimID: "claim-plan", Worker: d.auth.WorkerID, TTLMs: apitest.LeaseTTL,
	}, talkMember); err != nil {
		t.Fatal(err)
	}
	if _, err := assistant.StartLane(ctx, "wo-plan", api.StartLane{LaneID: "lane-plan"}); err != nil {
		t.Fatal(err)
	}
	var actions, cards []api.TalkEntry
	for _, entry := range d.entries(t, record.ID) {
		switch entry.Kind {
		case api.TalkKindAction:
			actions = append(actions, entry)
		case api.TalkKindCard:
			cards = append(cards, entry)
		}
	}
	if len(actions) != 2 || actions[0].Action.Operation != authority.OpWorkOrderCreate || actions[0].Action.Outcome != conversations.OutcomeRan ||
		actions[0].Action.Rev == 0 || actions[1].Action.Operation != authority.OpLaneStart || actions[1].Action.PlanningLane != "lane-plan" {
		t.Fatalf("actions %+v", actions)
	}
	if len(cards) != 1 || cards[0].Approval == nil || cards[0].Approval.State != authority.ApprovalPending || cards[0].Action.Approval != cards[0].Approval.ID {
		t.Fatalf("cards %+v", cards)
	}
}

// Where a member decided is kept: a card confirmed inline in a conversation on the web listener reads
// as the conversation, and one confirmed on the socket reads as the command line, whatever place it names.
func TestAPlaceRecordsWhereADecisionWasMade(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	_, assistant, _ := d.assistant(t)
	cardOf := func(id string) authority.Approval {
		t.Helper()
		createReady(t, d.Daemon, id)
		_, err := assistant.CancelWork(ctx, id, api.CancelWork{Reason: "Not now"})
		return preparedCard(t, err)
	}
	socketCard := cardOf("wo-a")
	if _, err := d.client.DecideApproval(ctx, socketCard.ID, api.DecideApproval{Digest: socketCard.SubjectDigest, Allow: false, Place: api.PlaceConversation}); err != nil {
		t.Fatal(err)
	}
	if read, err := d.Client.Approval(ctx, socketCard.ID); err != nil || read.Channel != authority.ChannelCLI {
		t.Fatalf("a card declined on the socket reads channel %q (%v)", read.Channel, err)
	}
	webCard := cardOf("wo-b")
	r := webOn(t, d.Daemon)
	cookies, token := r.signedIn(t)
	response, raw := r.send(t, call{
		method: http.MethodPost, path: apitest.Route("/v1/approvals/") + webCard.ID + "/decide", cookies: cookies,
		headers: map[string]string{api.HeaderCSRF: token},
		body:    api.DecideApproval{Digest: webCard.SubjectDigest, Allow: false, Place: api.PlaceConversation},
	})
	wantStatus(t, response, raw, http.StatusOK, "")
	if read, err := d.Client.Approval(ctx, webCard.ID); err != nil || read.Channel != authority.ChannelConversation {
		t.Fatalf("a card declined in a conversation reads channel %q (%v)", read.Channel, err)
	}
	response, raw = r.send(t, call{
		method: http.MethodPost, path: apitest.Route("/v1/approvals/") + webCard.ID + "/decide", cookies: cookies,
		headers: map[string]string{api.HeaderCSRF: token}, body: api.DecideApproval{Digest: webCard.SubjectDigest, Place: "sidebar"},
	})
	wantStatus(t, response, raw, http.StatusBadRequest, api.CodeInvalid)
}

// A card the assistant prepared names its conversation where a member reads it, and still does, with no
// name, once the conversation is deleted.
func TestACardNamesTheConversationThatPreparedIt(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	record, assistant, _ := d.assistant(t)
	createReady(t, d.Daemon, "wo-a")
	_, err := assistant.CancelWork(ctx, "wo-a", api.CancelWork{Reason: "Not now"})
	card := preparedCard(t, err)
	cardIn := func() *api.CardConversation {
		t.Helper()
		var view api.NeedsYou
		if err := d.client.ReadView(ctx, api.ViewNeedsYou, "", &view); err != nil {
			t.Fatal(err)
		}
		for _, item := range view.Items {
			if item.Card != nil && item.Card.Approval.ID == card.ID {
				return item.Card.Conversation
			}
		}
		t.Fatal("the card is not in Needs you")
		return nil
	}
	if named := cardIn(); named == nil || named.ID != record.ID || named.Name != conversations.DefaultName || named.Deleted {
		t.Fatalf("the card names %+v", named)
	}
	var detail api.ApprovalDetail
	if err := d.client.Do(ctx, http.MethodGet, apitest.Route("/v1/approvals/")+card.ID, nil, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Conversation == nil || detail.Conversation.ID != record.ID || detail.ID != card.ID {
		t.Fatalf("the detail reads %+v", detail)
	}
	if err := d.client.DeleteConversation(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if named := cardIn(); named == nil || named.Name != "" || !named.Deleted {
		t.Fatalf("after a delete the card names %+v", named)
	}
}

func TestTheAssistantPolicyIsPublishedAndBounded(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	if _, err := d.client.PublishPolicy(ctx, "[assistant]\nversion = 2\nidle_stop = \"30m\"\n"); err != nil {
		t.Fatal(err)
	}
	versions, err := d.client.AssistantPolicies(ctx)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 {
		t.Fatalf("versions %+v: %v", versions, err)
	}
	for _, body := range []string{
		"[assistant]\nversion = 3\nidle_stop = \"30s\"\n", "[assistant]\nversion = 3\nidle_stop = \"25h\"\n", "[assistant]\nversion = 3\n",
	} {
		_, err := d.client.PublishPolicy(ctx, body)
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	}
	targets, err := d.client.ConversationTargets(ctx)
	if err != nil || targets.Assistant.IdleStopMs != 30*60*1000 {
		t.Fatalf("targets read idle stop %d: %v", targets.Assistant.IdleStopMs, err)
	}
}

func TestAMessageToTheOrchestratorOrTheAssistantOnTheGeneralPath(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	_, err := d.client.SendMessage(ctx, messages.Draft{ID: "m1", To: messages.To(messages.TargetOrchestrator, d.auth.WorkerID, 0), Kind: messages.KindInstruction, Body: "Look"})
	refusedWith(t, err, http.StatusConflict, api.CodeTargetNotRunning)
	_, err = d.client.SendMessage(ctx, messages.Draft{ID: "m2", To: messages.To(messages.TargetAssistant, "conv-1", 0), Kind: messages.KindInstruction, Body: "Look"})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	if _, err := d.workers.SetOrchestrator(ctx, d.auth.WorkerID, ledger.OrchestratorRunning, talkMember); err != nil {
		t.Fatal(err)
	}
	sent, err := d.client.SendMessage(ctx, messages.Draft{ID: "m3", To: messages.To(messages.TargetOrchestrator, d.auth.WorkerID, 0), Kind: messages.KindInstruction, Body: "Look at WO-1"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(messages.Report{ID: sent.ID, State: messages.AtWorker})
	if err != nil {
		t.Fatal(err)
	}
	d.seq++
	if _, err := d.workers.Upload(ctx, ledger.UploadBatch{Auth: d.auth, Epoch: d.epoch, Events: []ledger.UploadEvent{{Epoch: d.epoch, Seq: d.seq, Type: ledger.UploadMessage, Payload: raw, OccurredAt: 1}}}); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(messages.Report{ID: sent.ID, State: messages.Replied, Reply: "Looking now."})
	if err != nil {
		t.Fatal(err)
	}
	d.seq++
	if _, err := d.workers.Upload(ctx, ledger.UploadBatch{Auth: d.auth, Epoch: d.epoch, Events: []ledger.UploadEvent{{Epoch: d.epoch, Seq: d.seq, Type: ledger.UploadMessage, Payload: raw, OccurredAt: 1}}}); err != nil {
		t.Fatal(err)
	}
	page, err := d.client.OrchestratorStream(ctx, d.auth.WorkerID, api.TalkQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, entry := range page.Entries {
		kinds = append(kinds, entry.Kind)
	}
	if len(page.Entries) != 2 || page.Entries[0].Kind != api.TalkKindMember || page.Entries[1].Kind != api.TalkKindReply || page.Entries[1].Body != "Looking now." {
		t.Fatalf("the orchestrator's stream holds %v: %+v", kinds, page.Entries)
	}
	targets, err := d.client.ConversationTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets.Orchestrators) != 1 || targets.Orchestrators[0].Name != "atlas" || targets.Orchestrators[0].Desired != ledger.OrchestratorRunning ||
		len(targets.Orchestrators[0].Projects) != 1 || len(targets.Assistant.Workers) != 1 {
		t.Fatalf("targets %+v", targets)
	}
}

// A headless lane's stream holds the member's messages to it with their delivery state, the events about
// it and its work, and its requests, oldest first, in pages that go back with `before`.
func TestALanesStreamPagesBack(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	w := apitest.NewWorld(t, d.Daemon)
	lane := w.Running("wo-1", "Add the retry budget")
	if _, err := d.client.SendMessage(ctx, messages.Draft{ID: "m-lane", To: messages.Target{Lane: lane.ID}, Kind: messages.KindInstruction, Body: "Use two retries"}); err != nil {
		t.Fatal(err)
	}
	page, err := d.client.LaneStream(ctx, lane.ID, api.TalkQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var member, events int
	for i, entry := range page.Entries {
		if i > 0 && entry.At < page.Entries[i-1].At {
			t.Fatalf("entry %d is older than the one before it", i)
		}
		switch entry.Kind {
		case api.TalkKindMember:
			member++
			if entry.Message == nil || entry.Message.State != messages.Queued || entry.Body != "Use two retries" {
				t.Errorf("the member's message reads %+v", entry)
			}
		case api.TalkKindEvent:
			events++
		}
	}
	if member != 1 || events < 2 {
		t.Fatalf("%d messages and %d events in %+v", member, events, page.Entries)
	}
	first, err := d.client.LaneStream(ctx, lane.ID, api.TalkQuery{Limit: 2})
	if err != nil || len(first.Entries) != 2 || first.Next == "" {
		t.Fatalf("a page of two: %+v %v", first, err)
	}
	older, err := d.client.LaneStream(ctx, lane.ID, api.TalkQuery{Limit: 2, Before: first.Next})
	if err != nil || len(older.Entries) == 0 || older.Entries[len(older.Entries)-1].At > first.Entries[0].At {
		t.Fatalf("an older page: %+v %v", older, err)
	}
	targets, err := d.client.ConversationTargets(ctx)
	if err != nil || len(targets.Lanes) != 1 || targets.Lanes[0].Lane != lane.ID || len(targets.Planning) != 0 {
		t.Fatalf("targets %+v: %v", targets, err)
	}
}

func TestTheAuditFiltersByActorAndSearchFindsConversations(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	if _, err := d.client.CreateConversation(ctx, api.CreateConversation{ID: "conv-1", Name: "Harbor merges"}); err != nil {
		t.Fatal(err)
	}
	page, err := d.client.Audit(ctx, api.AuditQuery{ActorID: d.auth.WorkerID})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range page.Events {
		if event.ActorID != d.auth.WorkerID {
			t.Errorf("the audit filtered by actor holds %+v", event.StreamEvent)
		}
	}
	var search api.Search
	if err := d.client.Do(ctx, http.MethodGet, apitest.Route(api.ViewPath(api.ViewSearch))+"?q=harbor+merges", nil, &search); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range search.Records {
		found = found || (record.Kind == api.SearchConversation && record.ID == "conv-1")
	}
	if !found {
		t.Fatalf("the search found %+v", search.Records)
	}
}

func TestSigningOutEverywhereStopsTheMembersConversations(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	_, _, grant := d.assistant(t)
	var out api.SignedOutAll
	if err := d.client.Do(ctx, http.MethodPost, api.SignOutAllPath, nil, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Conversations) != 1 || out.Conversations[0] != "conv-1" || !strings.HasPrefix(grant.Credential, authority.DelegateIDPrefix) {
		t.Fatalf("signing out stopped %+v", out)
	}
	err := d.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) error {
		record, err := tx.Conversations().Conversation("conv-1")
		if err == nil && (record.Running() || record.StoppedReason != conversations.ReasonSignedOut) {
			t.Errorf("the conversation reads %+v", record)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// assistantCard opens a conversation, runs its first generation, and has its assistant prepare a card that
// cancels a ready work order.
func (d *talkDaemon) assistantCard(t *testing.T, conversation, work string) (conversations.Conversation, ledger.AssistantGrant, authority.Approval) {
	t.Helper()
	ctx := t.Context()
	opened, err := d.client.CreateConversation(ctx, api.CreateConversation{ID: conversation})
	if err != nil {
		t.Fatal(err)
	}
	record := opened.Conversation.Conversation
	d.running(t, record)
	grant, err := d.workers.AssistantCredential(ctx, ledger.AssistantCredentialRequest{
		Auth: d.auth, Epoch: d.epoch, Conversation: record.ID, Lane: record.RunLane, Generation: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	createReady(t, d.Daemon, work)
	_, err = asDelegate(t, d.socket, grant.Token, apitest.ProjectName).CancelWork(ctx, work, api.CancelWork{Reason: "Not now"})
	return record, grant, preparedCard(t, err)
}

func (d *talkDaemon) cardState(t *testing.T, id string) authority.ApprovalState {
	t.Helper()
	var state authority.ApprovalState
	err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		row, err := tx.Approvals().Approval(id)
		state = row.State
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// An assistant's card stands after its credential ended routinely, with a stop, and not after a member
// revoked the credential, even where no sweep voided the card.
func TestAnAssistantsCardOutlivesARoutineEndAndNoRevocation(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	record, _, routine := d.assistantCard(t, "conv-1", "wo-a")
	if _, err := d.client.StopConversation(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.DecideApproval(ctx, routine.ID, api.DecideApproval{Digest: routine.SubjectDigest, Allow: true}); err != nil {
		t.Fatalf("a card of a stopped conversation: %v", err)
	}
	record, grant, revoked := d.assistantCard(t, "conv-2", "wo-b")
	if _, err := d.client.StopConversation(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewDelegates(d.Store).Revoke(ctx, apitest.Scope, grant.Credential, ledger.Actor{Kind: ledger.ActorMember, ID: memberOf(t, d.Daemon)}); err != nil {
		t.Fatal(err)
	}
	_, err := d.client.DecideApproval(ctx, revoked.ID, api.DecideApproval{Digest: revoked.SubjectDigest, Allow: true})
	refusedWith(t, err, http.StatusConflict, api.CodeRequestClosed)
	if order, err := d.Client.WorkOrder(ctx, "wo-b"); err != nil || order.State == workorders.Canceled {
		t.Errorf("a revoked assistant's card ran: %+v %v", order, err)
	}
}

// Signing out everywhere voids the open cards of every assistant credential of the member: an earlier
// generation's, a stopped conversation's, a deleted conversation's, and a running one's.
func TestSigningOutEverywhereVoidsEveryAssistantCard(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	earlier, _, first := d.assistantCard(t, "conv-1", "wo-a")
	if _, err := d.workers.AssistantCredential(ctx, ledger.AssistantCredentialRequest{
		Auth: d.auth, Epoch: d.epoch, Conversation: earlier.ID, Lane: earlier.RunLane, Generation: 2,
	}); err != nil {
		t.Fatal(err)
	}
	_, _, stopped := d.assistantCard(t, "conv-2", "wo-b")
	deleted, _, gone := d.assistantCard(t, "conv-3", "wo-c")
	if err := d.client.DeleteConversation(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}
	_, _, live := d.assistantCard(t, "conv-4", "wo-d")
	if err := d.client.Do(ctx, http.MethodPost, api.SignOutAllPath, nil, nil); err != nil {
		t.Fatal(err)
	}
	for name, card := range map[string]authority.Approval{"an earlier generation's": first, "a stopped conversation's": stopped, "a deleted conversation's": gone, "a running conversation's": live} {
		if state := d.cardState(t, card.ID); state != authority.ApprovalVoid {
			t.Errorf("%s card is %s", name, state)
		}
	}
}
