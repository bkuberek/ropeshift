package worker_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

// refusals counts what the worker said it refused to start, for the lanes whose names begin with prefix.
func (r *laneRig) refusals(prefix string) int {
	n := 0
	for _, problem := range r.problems() {
		if errors.Is(problem, worker.ErrCLINotQualified) && strings.HasPrefix(problem.Error(), "lane "+prefix) {
			n++
		}
	}
	return n
}

// ADR-0040 runs a lane only on a qualified agent CLI, and a worker told not to probe qualifies none. It is the
// worker of the daemon ropeshift init starts, which is stopped within seconds, so it starts no session: the lane
// is blocked with a reason that names why, and the work does not spend an attempt.
func TestAWorkerToldNotToProbeRefusesALane(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.config.NoProbe = true
	r.dispatched()
	r.runWith()
	if specs := r.driver.Specs(); len(specs) != 0 {
		t.Fatalf("%d sessions started on a worker told not to probe, the first for lane %s", len(specs), specs[0].Lane)
	}
	change := r.outcome()
	if change.Outcome != workorders.OutcomeBlocked || change.Reason != workorders.ReasonCLINotQualified || !strings.Contains(change.Detail, "qualif") {
		t.Errorf("outcome %+v", change)
	}
	if row := r.row(); row.State != workorders.Blocked || row.Attempts != 0 {
		t.Errorf("row %+v", row)
	}
	if n := r.refusals("lane-1"); n != 1 {
		t.Errorf("%d refusals reported for the lane, from %v", n, r.problems())
	}
}

// An orchestrator is a session too. It is refused as it begins, and does not wait for a wake to be refused.
func TestAWorkerToldNotToProbeRefusesAnOrchestrator(t *testing.T) {
	o := newOrchestratorRig(t, aSession())
	o.config.NoProbe = true
	o.start(t)
	deadline := time.Now().Add(10 * time.Second)
	for o.refusals("orchestrator-") == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the orchestrator was never refused: %v", o.problems())
		}
		o.tick(t)
	}
	if specs := o.driver.Specs(); len(specs) != 0 {
		t.Errorf("%d sessions started on a worker told not to probe, the first for lane %s", len(specs), specs[0].Lane)
	}
}

// An assistant is a session too: its conversation fails as its session is refused, and no session starts.
func TestAWorkerToldNotToProbeRefusesAnAssistant(t *testing.T) {
	a := newAssistantRig(t, aSession())
	a.config.NoProbe = true
	if _, err := a.convs.Create(t.Context(), scope, control.NewConversation{ID: "conv-1"}, member); err != nil {
		t.Fatal(err)
	}
	a.awaitAssistant(t, "the refusal of the assistant's session", func() bool {
		return a.conversation(t, "conv-1").Actual == conversations.Failed
	})
	if specs := a.assistantSpecs(); len(specs) != 0 {
		t.Errorf("%d assistant sessions started on a worker told not to probe", len(specs))
	}
}

// An orchestrator's consultation is a session too: it ends as failed, with the reason, and no session starts.
func TestAWorkerToldNotToProbeRefusesAConsultation(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	r.config.NoProbe = true
	r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
	ended := r.awaitConsulted(t, "q1")
	if ended.Outcome != ledger.ConsultationFailed || !strings.Contains(ended.Detail, "never qualifies the agent CLI") {
		t.Errorf("the consultation ended %+v", ended)
	}
	if specs := r.consultSpecs(); len(specs) != 0 {
		t.Errorf("%d consultation sessions started on a worker told not to probe", len(specs))
	}
}

// A worker told not to probe treats the agent CLI as unqualified, whatever its driver says of the version, and says
// so in the manifest it advertises. A worker that is not told is advertised as its driver reports it.
func TestAWorkerToldNotToProbeAdvertisesTheCLIAsUnqualified(t *testing.T) {
	t.Parallel()
	for _, told := range []bool{false, true} {
		base := newRig(t)
		subject := worker.New(base.link, base.journal, base.clock, worker.Config{
			Name:      "test",
			JoinToken: func(ctx context.Context) (string, error) { return base.plane.CreateJoinToken(ctx, member) },
			Lanes:     &worker.LaneRuntime{Driver: fake.NewAgentDriver(), NoProbe: told},
		})
		if err := subject.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		listed, err := base.plane.List(t.Context())
		if err != nil || len(listed) != 1 || len(listed[0].Manifest.Drivers) != 1 {
			t.Fatalf("workers %+v: %v", listed, err)
		}
		if got := listed[0].Manifest.Drivers[0].Qualified; got == told {
			t.Errorf("a worker told not to probe (%v) advertised its CLI as qualified (%v)", told, got)
		}
		subject.Wait()
	}
}
