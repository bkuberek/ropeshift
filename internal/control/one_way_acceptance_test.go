package control_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// logReads counts what a call reads of the installation's log, by the way it reads it.
type logReads struct {
	scans atomic.Int64
	finds atomic.Int64
}

// countingStore is a Store whose installation counts the reads of its log.
type countingStore struct {
	ports.Store
	reads *logReads
}

func (s countingStore) Installation(scope ledger.WorkspaceID) ports.Installation {
	return countingInstallation{Installation: s.Store.Installation(scope), reads: s.reads}
}

type countingInstallation struct {
	ports.Installation
	reads *logReads
}

func (i countingInstallation) Read(ctx context.Context, fn func(ports.SharedReadTx) error) error {
	return i.Installation.Read(ctx, func(tx ports.SharedReadTx) error {
		return fn(countingRead{SharedReadTx: tx, reads: i.reads})
	})
}

type countingRead struct {
	ports.SharedReadTx
	reads *logReads
}

// Events is a scan of the log: a page of it from a revision on.
func (r countingRead) Events(after int64, limit int) ([]ledger.Event, error) {
	r.reads.scans.Add(1)
	return r.SharedReadTx.Events(after, limit)
}

// Event is a read of one position of the log by its ID.
func (r countingRead) Event(id string) (ledger.Event, error) {
	r.reads.finds.Add(1)
	return r.SharedReadTx.Event(id)
}

// TestTheOneWayAcceptanceReadDoesNotScanTheInstallationsLog holds the read an orchestrator's record of a decision makes
// of its worker's one-way acknowledgement to the worker's own history: it finds the acts of that worker by their IDs, a
// few reads however long the installation's log is, and never lists the log. It stays correct across an acceptance, a
// withdrawal, and an acceptance again, for the orchestrator's decision and for the view a member reads (FR-QD-8).
func TestTheOneWayAcceptanceReadDoesNotScanTheInstallationsLog(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	reads := &logReads{}
	store := countingStore{Store: r.stores["pa"], reads: reads}
	ruled, autonomy := control.NewQuestions(store, questions.BatchPolicy{}), control.NewAutonomy(store)
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "worker-1"}
	decided := 0
	// Each decision binds a work order of its own, so none overlaps another.
	decide := func() error {
		decided++
		order := fmt.Sprintf("wo-%d", decided)
		r.ready("pa", order)
		_, err := ruled.RecordDecision(t.Context(), "pa", questions.Decision{
			ID: "decision-" + order, Status: questions.DecisionDecided, Statement: "One repository per service.",
			Rationale: "The plan fixes one repository per service.", Citations: []string{"wo:" + order},
			Scope: "wo:" + order, Reversibility: questions.OneWay, Categories: []string{questions.CategoryArchitecture},
		}, orchestrator)
		return err
	}
	withdraw := func() {
		t.Helper()
		if err := autonomy.WithdrawOneWay(t.Context(), "pa", "worker-1", projectMember); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct {
		name     string
		act      func()
		acts     int64
		standing bool
	}{
		{"before any act", func() {}, 0, false},
		{"an acceptance", func() { acceptOneWay(t, r) }, 1, true},
		{"a withdrawal", withdraw, 2, false},
		{"an acceptance again", func() { acceptOneWay(t, r) }, 3, true},
	}
	for _, step := range steps {
		step.act()
		scans, finds := reads.scans.Load(), reads.finds.Load()
		err := decide()
		if step.standing && err != nil {
			t.Errorf("%s: the orchestrator's one-way decision: %v", step.name, err)
		}
		if !step.standing && !errors.Is(err, questions.ErrOneWay) {
			t.Errorf("%s: the orchestrator's one-way decision: %v, want %v", step.name, err, questions.ErrOneWay)
		}
		view, err := autonomy.View(t.Context(), "pa", "worker-1")
		if err != nil {
			t.Fatal(err)
		}
		if (view.OneWay.Accepted != nil) != step.standing {
			t.Errorf("%s: the view reads %+v, want accepted %v", step.name, view.OneWay, step.standing)
		}
		if got := reads.scans.Load() - scans; got != 0 {
			t.Errorf("%s: the reads of the acknowledgement scanned the installation's log %d times, want none", step.name, got)
		}
		// Two reads of the history, each a read of every act of the worker and one more for the position after them.
		if got, most := reads.finds.Load()-finds, 2*(step.acts+1); got == 0 || got > most {
			t.Errorf("%s: the reads of the acknowledgement made %d reads by ID, want 1 to %d, which its %d acts account for", step.name, got, most, step.acts)
		}
	}
}
