package worker_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
)

// FR-ORCH-9: a worker keeps a consultation's start in its journal before the session starts, and the journal replaces
// it with the consultation's end in one write, so a restart finds a start only for a consultation whose end it never
// recorded.
func TestAConsultationIsJournaledFromBeforeItsSessionUntilItsEndIs(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	seen := make(chan []ledger.ConsultationRun, 1)
	r.nextSession([]fake.DriverStep{{Bound: true}, {Do: func(ports.SessionSpec) {
		kept, err := r.journal.Consultations(t.Context())
		if err != nil {
			t.Error(err)
		}
		seen <- kept
	}}, r.reportsConsultation(t, consultReport), completed(0.4)})
	started := r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
	r.awaitConsulted(t, "q1")
	kept := <-seen
	if len(kept) != 1 || kept[0].Grant.ID != started.Consultation || kept[0].Grant.Question != "q1" || kept[0].Grant.BudgetUSD != started.BudgetUSD ||
		kept[0].Orchestrator == "" {
		t.Errorf("the journal kept %+v while the session ran", kept)
	}
	left, err := r.journal.Consultations(t.Context())
	if err != nil || len(left) != 0 {
		t.Errorf("the journal still keeps %+v of a consultation that ended: %v", left, err)
	}
}

// leaveConsultation is what a worker that went away leaves of a consultation: the ledger holds its question and records
// its start, and the worker's journal keeps its start. It returns the grant, and a driver that finds the session left.
func (r *toolRig) leaveConsultation(t *testing.T, question, target string, left fake.DriverScript) (ledger.ConsultGrant, *fake.AgentDriver) {
	t.Helper()
	grant := r.grants(t, question, target)
	err := r.journal.BeginConsultation(t.Context(), ledger.ConsultationRun{Grant: grant, Orchestrator: r.row(t).Lane(), StartedAt: r.clock.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	driver := fake.NewAgentDriver()
	t.Cleanup(func() { endSessions(t, driver) })
	driver.Leave(consultLane+grant.ID, 1, lanes.Session{Found: true, Liveness: lanes.LivenessAlive})
	driver.Play(left)
	return grant, driver
}

// restarted is a new process of the worker, on its journal, whose driver is the one given.
func (r *toolRig) restarted(t *testing.T, driver *fake.AgentDriver) *worker.Worker {
	t.Helper()
	r.stopWorker()
	r.subject.Wait()
	config := *r.config
	config.Driver = driver
	next := worker.New(r.link, r.journal, r.clock, worker.Config{Name: "test", Lanes: &config})
	if err := next.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	return next
}

// FR-ORCH-9: a worker that went away while a consultation ran leaves its session running and spending, and the question
// held. The next start stops that session, records the consultation's end with what the session had cost, and lets the
// question go, so nothing runs on with nobody to read it.
func TestARestartedWorkerStopsAConsultationsSessionItsPredecessorLeft(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	grant, driver := r.leaveConsultation(t, "q1", "c-left", script([]fake.DriverStep{{Bound: true}, {Running: 0.7}, {Hold: true}}))
	next := r.restarted(t, driver)
	if err := next.MarkUnknown(t.Context()); err != nil {
		t.Fatal(err)
	}
	next.Wait()
	if err := next.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	ended := r.consultations(t, "q1")
	if len(ended) != 1 || ended[0].ID != grant.ID || ended[0].Outcome != ledger.ConsultationUnreported || ended[0].Recommendation != "" ||
		ended[0].CostUSD < 0.7-1e-9 || ended[0].CostUSD > 0.7+1e-9 || !strings.Contains(ended[0].Detail, "restart") {
		t.Fatalf("the consultation ended %+v", ended)
	}
	if ended[0].Agent != grant.Agent.Name || ended[0].Class != grant.Agent.Class {
		t.Errorf("the end names %s of %s, and the grant %s of %s", ended[0].Agent, ended[0].Class, grant.Agent.Name, grant.Agent.Class)
	}
	if adopted := driver.Adopted(); len(adopted) != 1 {
		t.Errorf("the session the worker left was adopted %d times", len(adopted))
	}
	if row := r.question(t, "q1"); row.ClaimedBy != "" {
		t.Errorf("the question is still held by %q", row.ClaimedBy)
	}
	if kept, err := r.journal.Consultations(t.Context()); err != nil || len(kept) != 0 {
		t.Errorf("the journal still keeps %+v: %v", kept, err)
	}
	if spent := r.workSpentToday(t); spent < 0.7-1e-9 {
		t.Errorf("the project's work was charged %v for the consultation", spent)
	}
}

// FR-ORCH-9: a consultation whose session is gone when the worker starts again still ends, and a start the worker cannot
// read the session of stays in the journal for the next, because the session may be running yet.
func TestARestartedWorkerEndsAConsultationWhoseSessionIsGoneAndKeepsOneItCannotRead(t *testing.T) {
	t.Parallel()
	t.Run("no session is left", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		grant, driver := r.leaveConsultation(t, "q1", "c-gone", script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
		driver.Leave(consultLane+grant.ID, 1, lanes.Session{})
		next := r.restarted(t, driver)
		if err := next.MarkUnknown(t.Context()); err != nil {
			t.Fatal(err)
		}
		next.Wait()
		if err := next.Upload(t.Context()); err != nil {
			t.Fatal(err)
		}
		if ended := r.consultations(t, "q1"); len(ended) != 1 || ended[0].Outcome != ledger.ConsultationUnreported || ended[0].CostUSD != 0 {
			t.Errorf("the consultation ended %+v", ended)
		}
		if row := r.question(t, "q1"); row.ClaimedBy != "" {
			t.Errorf("the question is still held by %q", row.ClaimedBy)
		}
	})
	t.Run("a session the worker cannot read", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		_, driver := r.leaveConsultation(t, "q1", "c-unread", script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
		driver.FailFind(errors.New("the listing did not answer"))
		next := r.restarted(t, driver)
		if err := next.MarkUnknown(t.Context()); err != nil {
			t.Fatal(err)
		}
		next.Wait()
		if kept, err := r.journal.Consultations(t.Context()); err != nil || len(kept) != 1 {
			t.Errorf("the journal keeps %+v: %v", kept, err)
		}
		if ended := r.consultations(t, "q1"); len(ended) != 0 {
			t.Errorf("a consultation whose session could not be read ended: %+v", ended)
		}
	})
}
