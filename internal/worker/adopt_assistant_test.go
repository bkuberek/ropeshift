package worker_test

import (
	"context"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/worker"
)

// adoptedAssistant runs an assistant to rest after its first prompt, then restarts the worker on a driver that
// holds that session, as a daemon restart finds it, and plays the rest of the session as plays.
func adoptedAssistant(t *testing.T, plays fake.DriverScript) (*assistantRig, *fake.AgentDriver) {
	t.Helper()
	first := script([]fake.DriverStep{{Bound: true}, {Reply: "Ready.", ReplyID: "msg_00"}, completed(0.01)}, []fake.DriverStep{{Hold: true}})
	a := newAssistantRig(t, first)
	if _, err := a.convs.Create(t.Context(), scope, control.NewConversation{ID: "conv-1"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "a session at rest", func() bool { return a.conversation(t, "conv-1").Actual == conversations.Running })
	specs := a.assistantSpecs()
	if len(specs) != 1 {
		t.Fatalf("assistant sessions %+v", specs)
	}
	previous := a.subject
	a.stopWorker()
	previous.Wait()
	driver := fake.NewAgentDriver()
	driver.Leave(specs[0].Lane, specs[0].Generation, sessionAlive)
	driver.Play(plays)
	config := *a.config
	config.Driver = driver
	next := worker.New(a.link, a.journal, a.clock, worker.Config{Name: "test", Lanes: &config, Report: a.noteProblem})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		driver.StopEverySession()
		next.Wait()
	})
	if err := next.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := next.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	a.subject, a.ctx, a.stopWorker = next, ctx, cancel
	return a, driver
}

// A session that an earlier worker started is a member's assistant after a restart as before it, so the driver
// is told to read its replies.
func TestAnAdoptedAssistantIsAskedForItsReplies(t *testing.T) {
	a, driver := adoptedAssistant(t, script([]fake.DriverStep{{Hold: true}}))
	a.awaitAssistant(t, "the session adopted", func() bool { return len(driver.Adopted()) == 1 })
	if spec := driver.Adopted()[0].Spec; !spec.Assistant {
		t.Errorf("the adopted session's spec says it is no assistant: %+v", spec)
	}
}

// What the first prompt's turn wrote is the answer to its brief, which no member reads. A restart does not
// make an assistant owe a brief again, so the reply a later turn wrote is the member's.
func TestAnAdoptedAssistantPostsItsNextReply(t *testing.T) {
	a, _ := adoptedAssistant(t, script([]fake.DriverStep{{Reply: "Two merges wait.", ReplyID: "msg_01"}, completed(0.01)}, []fake.DriverStep{{Hold: true}}))
	a.awaitAssistant(t, "a reply", func() bool { return len(a.replies(t, "conv-1")) != 0 })
	if got := a.replies(t, "conv-1"); !slices.Equal(got, []string{"Two merges wait."}) {
		t.Errorf("replies %q", got)
	}
}
