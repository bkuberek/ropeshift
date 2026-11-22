package worker_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/worker"
)

// restarted stops this worker and runs the next on the same journal, as a daemon restart does, and returns what
// that worker's first pass did.
func (o *orchestratorRig) restarted(t *testing.T) []lanes.Action {
	t.Helper()
	o.stopWorker()
	o.subject.Wait()
	config := *o.config
	next := worker.New(o.link, o.journal, o.clock, worker.Config{Name: "test", Lanes: &config, Report: o.noteProblem})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		o.driver.StopEverySession()
		next.Wait()
	})
	if err := next.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := next.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	o.subject, o.ctx, o.stopWorker = next, ctx, cancel
	actions, err := next.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return actions
}

// told sends a member's message to the orchestrator and returns a read of the state it is in.
func (o *orchestratorRig) told(t *testing.T, body string) (string, func() messages.State) {
	t.Helper()
	ctx, id, to := t.Context(), "m-restart", o.subject.Auth().WorkerID
	repository, err := control.NewRepositories(o.store).Detect(ctx, scope, control.DetectedRepository{ID: "repo-1", Directory: "harbor-api"}, member)
	if err != nil {
		t.Fatal(err)
	}
	sending := control.NewMessages(o.store, nil).Hosting(func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
		return map[string]control.AssistantHost{to: {Checkouts: map[string]bool{repository.ID: true}}}, nil
	})
	draft := messages.Draft{ID: id, To: messages.To(messages.TargetOrchestrator, to, 0), Kind: messages.KindInstruction, Body: body}
	if _, err := sending.Send(ctx, scope, draft, member); err != nil {
		t.Fatal(err)
	}
	return id, func() messages.State {
		row, err := sending.ReadMessage(ctx, scope, id)
		if err != nil {
			t.Fatal(err)
		}
		return row.State
	}
}

// awaitMessage passes until the message reads as it should.
func (o *orchestratorRig) awaitMessage(t *testing.T, state func() messages.State, want messages.State) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for state() != want && time.Now().Before(deadline) {
		o.tick(t)
	}
	if got := state(); got != want {
		t.Fatalf("the message reads %q, want %q", got, want)
	}
}

// A worker that stops while its orchestrator waits for something to judge leaves no session to adopt, so the next one
// continues the run from before its session (lanes.ActionContinue). That run hears a message as the first worker's did.
func TestAMessageWakesAnOrchestratorThatWaitedThroughARestart(t *testing.T) {
	o := newOrchestratorRig(t, aSession([]fake.DriverStep{{Hold: true}}))
	row := o.start(t)
	o.tick(t)
	if got := len(o.specs()); got != 0 {
		t.Fatalf("%d sessions before anything to judge", got)
	}
	actions := o.restarted(t)
	if !slices.Contains(actions, lanes.Action{Kind: lanes.ActionContinue, Lane: row.Lane()}) {
		t.Fatalf("the restart did %+v, and no session was there to adopt", actions)
	}
	id, state := o.told(t, "Are you there?")
	o.awaitNoted(t, 1)
	o.awaitMessage(t, state, messages.AtWorker)
	o.clock.Advance(31 * time.Second)
	o.awaitSessions(t, 1)
	if brief := o.specs()[0].Brief; !strings.Contains(brief, id) || !strings.Contains(brief, "Are you there?") {
		t.Errorf("the wake does not state the message: %q", brief)
	}
	o.awaitMessage(t, state, messages.HandedOff)
}

// A profile the worker cannot read leaves an orchestrator no bounds on its wakes. The worker reports that and wakes
// it on the default bounds, so the run meets the failure and ends saying so, and never waits in silence.
func TestAnOrchestratorWhoseProfileCannotBeReadStillWakes(t *testing.T) {
	for _, tt := range []struct {
		name      string
		restarted bool
	}{
		{name: "a fresh start"},
		{name: "a restart", restarted: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := newOrchestratorRig(t, aSession())
			profiles := o.config.Provisioner.Profiles.(*fake.ProfileSource)
			if !tt.restarted {
				profiles.PutUser("[lifecycle\n")
			}
			o.start(t)
			o.tick(t)
			if tt.restarted {
				profiles.PutUser("[lifecycle\n")
				o.restarted(t)
			}
			_, state := o.told(t, "Are you there?")
			o.awaitNoted(t, 1)
			o.awaitMessage(t, state, messages.AtWorker)
			o.clock.Advance(31 * time.Second)
			deadline := time.Now().Add(20 * time.Second)
			for o.row(t).Actual != "ended" && time.Now().Before(deadline) {
				o.tick(t)
			}
			if row := o.row(t); row.Actual != "ended" {
				t.Fatalf("the run waits on a profile nobody can read: %+v", row)
			}
			if !slices.ContainsFunc(o.problems(), func(err error) bool { return strings.Contains(err.Error(), "profile") }) {
				t.Errorf("the worker reported %v", o.problems())
			}
		})
	}
}
