package worker_test

import (
	"encoding/json"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/lanes"
)

// A clear inside an assistant's process begins a generation whose first prompt is the brief the worker sends again, and
// what the session writes in answer to it is the answer to a brief, which no member reads: only the turns after it hold
// replies.
func TestAnAssistantsReBriefAfterAClearIsNoReply(t *testing.T) {
	a := newAssistantRig(t, script(
		[]fake.DriverStep{{Bound: true}, completed(0.01)},
		[]fake.DriverStep{{Reply: "I have my brief.", ReplyID: "msg_01"}, completed(0.01)},
		[]fake.DriverStep{{Reply: "Two merges wait.", ReplyID: "msg_02"}, completed(0.01)},
		[]fake.DriverStep{{Hold: true}},
	))
	ctx := t.Context()
	if _, err := a.convs.Create(ctx, scope, control.NewConversation{ID: "conv-1"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "a session at rest", func() bool { return a.conversation(t, "conv-1").Actual == conversations.Running })

	line, err := json.Marshal(lanes.Request{
		Version: lanes.SocketProtocol, Token: tokenIn(a.assistantSpecs()[0]),
		Hook: &lanes.HookEvent{Event: "SessionStart", Subject: clearedSession, SessionID: clearedSession, Detail: lanes.SourceClear, AtMs: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply := a.subject.AnswerLane(ctx, line); !reply.OK || reply.Generation != 2 {
		t.Fatalf("the clear was answered %+v", reply)
	}
	a.awaitAssistant(t, "the re-brief", func() bool { return len(a.driver.Sent()) == 1 })
	for range 5 {
		a.tick(t)
	}
	if got := a.replies(t, "conv-1"); len(got) != 0 {
		t.Fatalf("the answer to the brief after a clear was recorded as a reply: %q", got)
	}

	if _, err := a.convs.Send(ctx, scope, "conv-1", control.MemberMessage{ID: "m-1", Body: "Which merges wait?"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "a reply", func() bool { return len(a.replies(t, "conv-1")) != 0 })
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		a.tick(t)
	}
	if got := a.replies(t, "conv-1"); len(got) != 1 || got[0] != "Two merges wait." {
		t.Errorf("replies %q, want the answer to the member alone", got)
	}
}
