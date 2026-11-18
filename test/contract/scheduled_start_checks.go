package contract

import (
	"encoding/json"
	"errors"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// ScheduledStartStoreChecks hold the record of a start a member scheduled for a sign-in's usage reset: one per worker,
// replaced by the next, removed once, and kept across a restart, in the installation's records.
var ScheduledStartStoreChecks = []Check{
	{"OS1", "a scheduled start is one per worker, and the next replaces it", checkScheduledStartOnePerWorker},
	{"OS2", "a scheduled start is removed once", checkScheduledStartRemovedOnce},
	{"OS3", "a scheduled start survives a restart", checkScheduledStartSurvivesRestart},
}

func (r *Rig) scheduleStart(s ports.Store, start ledger.ScheduledStart) error {
	return s.Installation(r.Scope).Write(r.Ctx, "test.scheduled_start", func(tx ports.SharedTx) error {
		if err := tx.Workers().SaveScheduledStart(start); err != nil {
			return err
		}
		return appendStartEvent(tx, ledger.EventOrchestratorStartScheduled, start, r.Actor)
	})
}

func appendStartEvent(tx ports.SharedTx, eventType string, start ledger.ScheduledStart, actor ledger.Actor) error {
	data, err := json.Marshal(start)
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{Type: eventType, Actor: actor, Data: data, Subject: ledger.Subject{Kind: ledger.SubjectOrchestrator, ID: ledger.OrchestratorOf(start.Worker)}})
	return err
}

func (r *Rig) scheduledStarts(s ports.Store) ([]ledger.ScheduledStart, error) {
	var out []ledger.ScheduledStart
	err := s.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
		var err error
		out, err = tx.Workers().ScheduledStarts()
		return err
	})
	return out, err
}

func checkScheduledStartOnePerWorker(r *Rig) error {
	s := r.Store()
	first := ledger.ScheduledStart{Worker: "worker-1", StartsAt: 5_000, ScheduledBy: "member-1", ScheduledAt: 1_000}
	if err := r.scheduleStart(s, first); err != nil {
		return err
	}
	if err := r.scheduleStart(s, ledger.ScheduledStart{Worker: "worker-2", ScheduledBy: "member-1", ScheduledAt: 1_000}); err != nil {
		return err
	}
	next := first
	next.StartsAt, next.Missed = 9_000, 1
	if err := r.scheduleStart(s, next); err != nil {
		return err
	}
	starts, err := r.scheduledStarts(s)
	if err != nil {
		return err
	}
	if len(starts) != 2 || starts[0] != next || starts[1].Worker != "worker-2" || starts[1].StartsAt != 0 {
		return Violatedf("the scheduled starts read %+v", starts)
	}
	return s.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
		one, err := tx.Workers().ScheduledStart("worker-1")
		if err != nil || one != next {
			return Violatedf("worker-1's scheduled start reads %+v, %v", one, err)
		}
		if _, err := tx.Workers().ScheduledStart("worker-3"); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("a worker with no scheduled start: %v", err)
		}
		return nil
	})
}

func checkScheduledStartRemovedOnce(r *Rig) error {
	s := r.Store()
	if err := r.scheduleStart(s, ledger.ScheduledStart{Worker: "worker-1", StartsAt: 5_000, ScheduledBy: "member-1", ScheduledAt: 1_000}); err != nil {
		return err
	}
	var removed []bool
	for range 2 {
		err := s.Installation(r.Scope).Write(r.Ctx, "test.unschedule", func(tx ports.SharedTx) error {
			gone, err := tx.Workers().DeleteScheduledStart("worker-1")
			removed = append(removed, gone)
			if err != nil || !gone {
				return err
			}
			return appendStartEvent(tx, ledger.EventOrchestratorStartEnded, ledger.ScheduledStart{Worker: "worker-1"}, r.Actor)
		})
		if err != nil {
			return err
		}
	}
	if !removed[0] || removed[1] {
		return Violatedf("two removals reported %v", removed)
	}
	return nil
}

func checkScheduledStartSurvivesRestart(r *Rig) error {
	if !r.DB.Restartable {
		return nil
	}
	s := r.Store()
	start := ledger.ScheduledStart{Worker: "worker-1", StartsAt: 5_000, ScheduledBy: "member-1", ScheduledAt: 1_000}
	if err := r.scheduleStart(s, start); err != nil {
		return err
	}
	if err := s.Close(); err != nil {
		return err
	}
	starts, err := r.scheduledStarts(r.Store())
	if err != nil {
		return err
	}
	if len(starts) != 1 || starts[0] != start {
		return Violatedf("after a restart the scheduled starts read %+v", starts)
	}
	return nil
}
