package worker_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
)

// journaledWhile reads the consultations the journal keeps until what it reads is what a test waits for, or a bound
// passes, and returns the last read. A session runs ahead of the worker that reads it, so what the worker keeps of an
// event lands a moment after the session sent it.
func (r *toolRig) journaledWhile(t *testing.T, done func([]ledger.ConsultationRun) bool) []ledger.ConsultationRun {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		kept, err := r.journal.Consultations(t.Context())
		if err != nil {
			t.Error(err)
		}
		if done(kept) || time.Now().After(deadline) {
			return kept
		}
		time.Sleep(superviseStep)
	}
}

// FR-ORCH-9: a worker keeps a consultation's start in its journal before the session starts, with what the session has
// cost as the worker reads it, and the journal replaces it with the consultation's end in one write, so a restart finds a
// start only for a consultation whose end it never recorded, and knows what that consultation spent.
func TestAConsultationIsJournaledFromBeforeItsSessionUntilItsEndIs(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	seen := make(chan []ledger.ConsultationRun, 1)
	r.nextSession([]fake.DriverStep{{Bound: true}, {Running: 0.3}, {Do: func(ports.SessionSpec) {
		seen <- r.journaledWhile(t, func(kept []ledger.ConsultationRun) bool { return len(kept) == 1 && kept[0].CostUSD >= 0.3 })
	}}, r.reportsConsultation(t, consultReport), completed(0.4)})
	started := r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
	r.awaitConsulted(t, "q1")
	kept := <-seen
	if len(kept) != 1 || kept[0].Grant.ID != started.Consultation || kept[0].Grant.Question != "q1" || kept[0].Grant.BudgetUSD != started.BudgetUSD ||
		kept[0].Orchestrator == "" || math.Abs(kept[0].CostUSD-0.3) > 1e-9 {
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
// held. The next start stops that session, records the consultation's end with what the session had cost before the
// worker went away, which the journal kept, and lets the question go, so nothing runs on with nobody to read it. A session
// stopped at once may say nothing more of its cost, so what the journal kept is what the end counts.
func TestARestartedWorkerStopsAConsultationsSessionItsPredecessorLeft(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	grant, driver := r.leaveConsultation(t, "q1", "c-left", script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	if err := r.journal.SaveConsultationCost(t.Context(), grant.ID, 0.7); err != nil {
		t.Fatal(err)
	}
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
	if adopted := driver.Adopted(); len(adopted) != 1 || math.Abs(adopted[0].Cost.SessionTotalUSD-0.7) > 1e-9 {
		t.Errorf("the session the worker left was adopted as %+v", adopted)
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

// FR-ORCH-9: a session a worker that went away left may have spent past what the journal kept of its cost, and what it
// streamed since says how much. The next start reads what the session streamed before it stops it, so the stop loses
// none of that spend, and the consultation's end counts it.
func TestARestartedWorkerReadsALeftSessionBeforeItStopsIt(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	grant, driver := r.leaveConsultation(t, "q1", "c-spent", script([]fake.DriverStep{{Bound: true}, {Running: 0.9}, {Hold: true}}))
	if err := r.journal.SaveConsultationCost(t.Context(), grant.ID, 0.7); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var keptAtStop []float64
	driver.OnStop(func() {
		kept, err := r.journal.Consultations(context.Background())
		mu.Lock()
		defer mu.Unlock()
		if err == nil && len(kept) == 1 {
			keptAtStop = append(keptAtStop, kept[0].CostUSD)
		}
	})
	next := r.restarted(t, driver)
	if err := next.MarkUnknown(t.Context()); err != nil {
		t.Fatal(err)
	}
	next.Wait()
	if err := next.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	stops := slices.Clone(keptAtStop)
	mu.Unlock()
	if len(stops) == 0 || math.Abs(stops[0]-0.9) > 1e-9 {
		t.Errorf("the journal kept costs of %v at each stop, and the session had streamed $0.90 before the first", stops)
	}
	ended := r.consultations(t, "q1")
	if len(ended) != 1 || ended[0].Outcome != ledger.ConsultationUnreported || math.Abs(ended[0].CostUSD-0.9) > 1e-9 {
		t.Fatalf("the consultation ended %+v", ended)
	}
	if spent := r.workSpentToday(t); spent < 0.9-1e-9 {
		t.Errorf("the project's work was charged %v for the consultation", spent)
	}
}

// FR-ORCH-9: a consultation whose session is gone when the worker starts again still ends, with what the journal kept of
// its cost, and a start the worker cannot read the session of stays in the journal for the next, because the session may
// be running yet.
func TestARestartedWorkerEndsAConsultationWhoseSessionIsGoneAndKeepsOneItCannotRead(t *testing.T) {
	t.Parallel()
	t.Run("no session is left", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		grant, driver := r.leaveConsultation(t, "q1", "c-gone", script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
		driver.Leave(consultLane+grant.ID, 1, lanes.Session{})
		if err := r.journal.SaveConsultationCost(t.Context(), grant.ID, 0.25); err != nil {
			t.Fatal(err)
		}
		next := r.restarted(t, driver)
		if err := next.MarkUnknown(t.Context()); err != nil {
			t.Fatal(err)
		}
		next.Wait()
		if err := next.Upload(t.Context()); err != nil {
			t.Fatal(err)
		}
		if ended := r.consultations(t, "q1"); len(ended) != 1 || ended[0].Outcome != ledger.ConsultationUnreported || math.Abs(ended[0].CostUSD-0.25) > 1e-9 {
			t.Errorf("the consultation ended %+v", ended)
		}
		if spent := r.workSpentToday(t); spent < 0.25-1e-9 {
			t.Errorf("the project's work was charged %v for the consultation", spent)
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
