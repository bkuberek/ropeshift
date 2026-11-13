package server_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestGoldenConversationReads writes what the conversation surface reads, from a real ledger on a clock
// that repeats: a member's conversation with the assistant, which answered with citations, ran an
// operation, prepared two cards, one of which the member confirmed, and started a planning session; the
// member's "do it" while a card waited; what the member can talk to; and the streams of a lane and of an
// orchestrator. A test of the page runs on what the API serves.
func TestGoldenConversationReads(t *testing.T) {
	clock := apitest.NewClock()
	d := startTalkWith(t, apitest.Options{Clock: clock.Dialect})
	ctx := t.Context()
	record, assistant, _ := d.assistant(t)
	reply := func(id, body string) {
		t.Helper()
		clock.Advance(time.Second)
		if err := d.workers.AssistantEntry(ctx, ledger.AssistantEntryRequest{
			Auth: d.auth, Epoch: d.epoch, Project: apitest.ProjectName, Conversation: record.ID, Lane: record.RunLane, ID: id, Body: body,
		}); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Second)
	if _, err := d.client.SendToAssistant(ctx, record.ID, api.ConversationMessage{ID: "msg-ask", Body: "Cancel the retry work, then plan the harbor work."}); err != nil {
		t.Fatal(err)
	}
	reply("vendor-1", "The retry work is [[work_order:wo-a]], and its code is at [[code:harbor-api:src/retry.go#L42@a1b2c3d]].\n\nCancelling it is your decision, so I prepared a card.")
	createReady(t, d.Daemon, "wo-a")
	clock.Advance(time.Second)
	_, err := assistant.CancelWork(ctx, "wo-a", api.CancelWork{Reason: "The retry work is not needed."})
	preparedCard(t, err)
	clock.Advance(time.Second)
	if _, err := d.client.SendToAssistant(ctx, record.ID, api.ConversationMessage{ID: "msg-yes", Body: "do it"}); err != nil {
		t.Fatal(err)
	}
	createReady(t, d.Daemon, "wo-b")
	clock.Advance(time.Second)
	_, err = assistant.CancelWork(ctx, "wo-b", api.CancelWork{Reason: "It duplicates wo-a."})
	decided := preparedCard(t, err)
	clock.Advance(time.Second)
	if _, err := d.client.DecideApproval(ctx, decided.ID, api.DecideApproval{Digest: decided.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	created, err := assistant.CreateWorkOrder(ctx, api.CreateWorkOrder{
		ID: "wo-plan", Kind: workorders.KindPlan, Title: "Plan the harbor", Criteria: []planning.Criterion{{ID: "c1", Text: "A plan"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.Review(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewLeases(d.Store).Claim(ctx, apitest.Scope, workorders.ClaimRequest{
		WorkOrder: created.ID, ClaimID: "claim-plan", Worker: d.auth.WorkerID, TTLMs: apitest.LeaseTTL,
	}, talkMember); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if _, err := assistant.StartLane(ctx, created.ID, api.StartLane{LaneID: "lane-plan"}); err != nil {
		t.Fatal(err)
	}
	reply("vendor-2", "I started a planning session on [[work_order:wo-plan]].")
	w := apitest.NewWorld(t, d.Daemon)
	lane := w.Running("wo-1", "Add the retry budget")
	clock.Advance(time.Second)
	if _, err := d.client.SendMessage(ctx, messages.Draft{ID: "msg-lane", To: messages.Target{Lane: lane.ID}, Kind: messages.KindInstruction, Body: "Use two retries"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.SendMessage(ctx, messages.Draft{ID: "msg-orchestrator", To: messages.Target{Kind: messages.TargetOrchestrator, Worker: "worker-atlas"}, Kind: messages.KindInstruction, Body: "Claim the harbor work first"}); err == nil {
		t.Log("the orchestrator took a message")
	}
	reads := map[string]func() (any, error){
		"conversations": func() (any, error) { return d.client.Conversations(ctx) },
		"conversation-entries": func() (any, error) {
			return d.client.ConversationEntries(ctx, record.ID, api.TalkQuery{Limit: api.TalkMaxLimit})
		},
		"conversation-targets": func() (any, error) { return d.client.ConversationTargets(ctx) },
		"lane-stream":          func() (any, error) { return d.client.LaneStream(ctx, lane.ID, api.TalkQuery{}) },
		"orchestrator-stream":  func() (any, error) { return d.client.OrchestratorStream(ctx, "worker-atlas", api.TalkQuery{}) },
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			value, err := read()
			if err != nil {
				t.Fatal(err)
			}
			holdsGolden(t, name, steadyCredentials(steady(pretty(t, value))))
		})
	}
}
