package worker_test

import (
	"context"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
)

// sessionFinds is a driver that keeps the spec each look for a session an earlier worker left is given.
type sessionFinds struct {
	ports.AgentDriver
	mu    sync.Mutex
	specs []ports.SessionSpec
}

func (d *sessionFinds) Find(ctx context.Context, spec ports.SessionSpec) (ports.Located, error) {
	d.mu.Lock()
	d.specs = append(d.specs, spec)
	d.mu.Unlock()
	return d.AgentDriver.Find(ctx, spec)
}

func (d *sessionFinds) found() []ports.SessionSpec {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]ports.SessionSpec{}, d.specs...)
}

// An orchestrator's lane records no worktree, because its session runs in a private directory of its own, so a
// restarted worker looks for that session where it runs, and never in an empty directory, which a driver's listing of
// live sessions would read as another one.
func TestAnOrchestratorsSessionIsLookedForWhereItRuns(t *testing.T) {
	t.Parallel()
	w := newWorldRig(t)
	err := w.journal.SaveLane(t.Context(), lanes.Record{ID: "orchestrator", Generation: 1, State: lanes.StateRunning, Owner: lanes.OwnerOrchestrator})
	if err != nil {
		t.Fatal(err)
	}
	finds := &sessionFinds{AgentDriver: w.driver}
	w.config.Driver = finds
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	specs := finds.found()
	if len(specs) == 0 {
		t.Fatal("the restarted worker never looked for the orchestrator's session")
	}
	if want := worker.OrchestratorDir(w.config, "orchestrator"); specs[0].Lane != "orchestrator" || specs[0].Dir != want {
		t.Errorf("the orchestrator's session was looked for in %q, and it runs in %q", specs[0].Dir, want)
	}
}

// An assistant's lane records no worktree either, because its session runs in its conversation's directory, which every
// run of that conversation shares, so a restarted worker looks for the session there.
func TestAnAssistantsSessionIsLookedForWhereItRuns(t *testing.T) {
	t.Parallel()
	w := newWorldRig(t)
	ctx := t.Context()
	desired := ledger.DesiredState{Rev: 1, Assistants: []ledger.DesiredAssistant{{Conversation: "conversation-1", Member: "member-1", Lane: "assistant-1", Generation: 1}}}
	if err := w.journal.SaveDesired(ctx, desired); err != nil {
		t.Fatal(err)
	}
	err := w.journal.SaveLane(ctx, lanes.Record{ID: "assistant-1", Generation: 1, State: lanes.StateRunning, Owner: worker.OwnerAssistant})
	if err != nil {
		t.Fatal(err)
	}
	finds := &sessionFinds{AgentDriver: w.driver}
	w.config.Driver = finds
	if _, err := w.subject.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	specs := finds.found()
	if len(specs) == 0 {
		t.Fatal("the restarted worker never looked for the assistant's session")
	}
	if want := worker.AssistantDir(w.config, "conversation-1"); specs[0].Lane != "assistant-1" || specs[0].Dir != want {
		t.Errorf("the assistant's session was looked for in %q, and it runs in %q", specs[0].Dir, want)
	}
}
