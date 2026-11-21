package worker_test

import (
	"context"
	"math"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
)

// consultJournal stands between a worker and its journal at a consultation's start: it runs what a test gives it once a
// start is journaled.
type consultJournal struct {
	*fake.Journal
	mu      sync.Mutex
	onBegin func(ledger.ConsultationRun)
}

func (j *consultJournal) BeginConsultation(ctx context.Context, run ledger.ConsultationRun) error {
	j.mu.Lock()
	onBegin := j.onBegin
	j.mu.Unlock()
	if err := j.Journal.BeginConsultation(ctx, run); err != nil {
		return err
	}
	if onBegin != nil {
		onBegin(run)
	}
	return nil
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
