package worker_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// consultJournal stands between a worker and its journal at a consultation's start and end: it runs what a test gives
// it once a start is journaled, and fails a start or an end when a test says so.
type consultJournal struct {
	*fake.Journal
	mu        sync.Mutex
	begun     []ledger.ConsultationRun
	onBegin   func(ledger.ConsultationRun)
	failBegin error
	failEnd   error
}

func (j *consultJournal) BeginConsultation(ctx context.Context, run ledger.ConsultationRun) error {
	j.mu.Lock()
	j.begun = append(j.begun, run)
	onBegin, fail := j.onBegin, j.failBegin
	j.mu.Unlock()
	if fail != nil {
		return fail
	}
	if err := j.Journal.BeginConsultation(ctx, run); err != nil {
		return err
	}
	if onBegin != nil {
		onBegin(run)
	}
	return nil
}

func (j *consultJournal) EndConsultation(ctx context.Context, id string, event ledger.UploadEvent) (ledger.UploadEvent, bool, error) {
	j.mu.Lock()
	fail := j.failEnd
	j.mu.Unlock()
	if fail != nil {
		return ledger.UploadEvent{}, false, fail
	}
	return j.Journal.EndConsultation(ctx, id, event)
}

func (j *consultJournal) starts() []ledger.ConsultationRun {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]ledger.ConsultationRun(nil), j.begun...)
}

// aConsultJournalRig is a tool rig whose worker keeps its consultations in a journal the test stands between.
func aConsultJournalRig(t *testing.T) (*toolRig, *consultJournal) {
	t.Helper()
	base := newRig(t)
	journal := &consultJournal{Journal: base.journal}
	base.workerJournal = journal
	return aToolRigOn(t, base), journal
}

// FR-ORCH-9: a worker takes a consultation before its start reaches the journal, so a recovery that reads the journal
// while the session runs finds the start and leaves it to the process that runs it, which ends it with what its session
// reported and cost.
func TestARecoveryLeavesAConsultationThisProcessRuns(t *testing.T) {
	t.Parallel()
	r, journal := aConsultJournalRig(t)
	taken := make(chan bool, 1)
	journal.mu.Lock()
	journal.onBegin = func(run ledger.ConsultationRun) {
		taken <- r.subject.ConsultationTaken(run.Grant.ID)
		r.subject.RecoverConsultations(r.ctx)
	}
	journal.mu.Unlock()
	r.nextSession([]fake.DriverStep{{Bound: true}, r.reportsConsultation(t, consultReport), completed(0.4)})
	r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
	if !<-taken {
		t.Error("the consultation's start reached the journal before this process took it")
	}
	ended := r.awaitConsulted(t, "q1")
	if ended.Outcome != ledger.ConsultationReported || math.Abs(ended.CostUSD-0.4) > 1e-9 {
		t.Errorf("the consultation ended %+v", ended)
	}
	if all := r.consultations(t, "q1"); len(all) != 1 {
		t.Errorf("the consultation ended %d times: %+v", len(all), all)
	}
}

// FR-ORCH-9: a journal that cannot keep a consultation's start or its end leaves the ledger holding the question with
// nothing to end the hold but its own end. The worker records the end as an event of its own instead: a start it could
// not keep ends as a failure that cost nothing, with no session started, and an end it could not keep with the start
// still reaches the ledger, so the question is let go of now.
func TestAJournalThatFailsAConsultationStillEndsIt(t *testing.T) {
	t.Parallel()
	t.Run("the start", func(t *testing.T) {
		t.Parallel()
		r, journal := aConsultJournalRig(t)
		journal.mu.Lock()
		journal.failBegin = errors.New("the disk is full")
		journal.mu.Unlock()
		r.nextSession([]fake.DriverStep{{Bound: true}, r.reportsConsultation(t, consultReport), completed(0.4)})
		if reply := r.tool(t, lanes.ToolConsult, map[string]any{"question": "q1", "ask": consultAsk}); reply.OK {
			t.Fatalf("a consultation whose start the journal could not keep started: %+v", reply)
		}
		begun := journal.starts()
		if len(begun) != 1 {
			t.Fatalf("the worker journaled %d starts", len(begun))
		}
		if r.subject.ConsultationTaken(begun[0].Grant.ID) {
			t.Error("this process still holds a consultation it never ran")
		}
		if err := r.subject.Upload(t.Context()); err != nil {
			t.Fatal(err)
		}
		ended := r.consultations(t, "q1")
		if len(ended) != 1 || ended[0].ID != begun[0].Grant.ID || ended[0].Outcome != ledger.ConsultationFailed || ended[0].CostUSD != 0 ||
			!strings.Contains(ended[0].Detail, "journal") {
			t.Errorf("the consultation ended %+v", ended)
		}
		if row := r.question(t, "q1"); row.ClaimedBy != "" {
			t.Errorf("the question is still held by %q", row.ClaimedBy)
		}
		for _, spec := range r.driver.Specs() {
			if strings.HasPrefix(spec.Lane, consultLane) {
				t.Errorf("a session started for a consultation whose start the journal could not keep: %s", spec.Lane)
			}
		}
	})
	t.Run("the end", func(t *testing.T) {
		t.Parallel()
		r, journal := aConsultJournalRig(t)
		journal.mu.Lock()
		journal.failEnd = errors.New("the disk is full")
		journal.mu.Unlock()
		r.nextSession([]fake.DriverStep{{Bound: true}, r.reportsConsultation(t, consultReport), completed(0.4)})
		r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
		ended := r.awaitConsulted(t, "q1")
		if ended.Outcome != ledger.ConsultationReported || math.Abs(ended.CostUSD-0.4) > 1e-9 {
			t.Errorf("the consultation ended %+v", ended)
		}
		if row := r.question(t, "q1"); row.ClaimedBy != "" {
			t.Errorf("the question is still held by %q", row.ClaimedBy)
		}
	})
}
