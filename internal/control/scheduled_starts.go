package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// signInUsageOf reads what a worker last said of its sign-in's usage limit, or nil when it said nothing.
func signInUsageOf(worker ledger.Worker) *ledger.SignInUsage {
	if worker.Report == nil {
		return nil
	}
	return worker.Report.SignIn
}

// orchestratorFacts says what keeps a worker's orchestrator from starting or running now, and the start a member
// scheduled for it, as every surface reads them.
func orchestratorFacts(workers ports.WorkerReader, worker ledger.Worker, heartbeatMs, now int64) (*ledger.OrchestratorHold, *ledger.ScheduledStart, error) {
	var row *ledger.Orchestrator
	found, err := workers.Orchestrator(worker.ID)
	switch {
	case err == nil:
		row = &found
	case !errors.Is(err, ports.ErrNotFound):
		return nil, nil, err
	}
	usage := signInUsageOf(worker)
	var readAt int64
	if usage != nil {
		readAt = worker.ReportedAt - usage.ReadAgeMs
	}
	hold := ledger.OrchestratorHoldOf(connected(worker, now, heartbeatMs), row, usage, readAt, now)
	start, err := workers.ScheduledStart(worker.ID)
	switch {
	case err == nil:
		return hold, &start, nil
	case errors.Is(err, ports.ErrNotFound):
		return hold, nil, nil
	}
	return nil, nil, err
}

// OrchestratorFacts reads what holds a worker's orchestrator, and the start a member scheduled for it.
func (w *Workers) OrchestratorFacts(tx ports.ReadTx, worker ledger.Worker) (*ledger.OrchestratorHold, *ledger.ScheduledStart, error) {
	now, err := tx.Now()
	if err != nil {
		return nil, nil, err
	}
	return orchestratorFacts(tx.Workers(), worker, w.options.HeartbeatMs, now)
}

// appendStart records a scheduled start's change in the installation's log, about its orchestrator.
func appendStart(tx ports.SharedTx, eventType string, start ledger.ScheduledStart, actor ledger.Actor) error {
	data, err := json.Marshal(start)
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{Type: eventType, Actor: actor, Data: data, Subject: ledger.Subject{Kind: ledger.SubjectOrchestrator, ID: ledger.OrchestratorOf(start.Worker)}})
	return err
}

// scheduleStart records a start of a worker's orchestrator for its sign-in's usage reset, which a member asks for by
// pressing Start while the limit holds. It waits for the reset the worker last reported, or, with none ahead, for the
// next one reported. A second schedules it again in its place.
func scheduleStart(ctx context.Context, installation ports.Installation, worker string, actor ledger.Actor) (ledger.ScheduledStart, error) {
	if !authority.Allowed(actor, authority.ActionRunOrchestrator) {
		return ledger.ScheduledStart{}, workorders.ErrForbidden
	}
	var start ledger.ScheduledStart
	err := installation.Write(ctx, "orchestrator.schedule", func(tx ports.SharedTx) error {
		record, err := tx.Workers().Find(worker)
		if err != nil {
			return err
		}
		row, err := tx.Workers().Orchestrator(worker)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		switch {
		case err == nil && row.Desired == ledger.OrchestratorRunning && row.Tripped():
			return fmt.Errorf("%w: the breaker of worker %s's orchestrator tripped, so reset it with orchestrator start", workorders.ErrInvalid, worker)
		case err == nil && row.Desired == ledger.OrchestratorRunning:
			return fmt.Errorf("%w: worker %s's orchestrator is already asked to run, and it runs once the limit resets", workorders.ErrInvalid, worker)
		}
		usage := signInUsageOf(record.Worker)
		if usage == nil || !usage.Limited {
			return fmt.Errorf("%w: the sign-in of worker %s is not at its usage limit, so start its orchestrator now", workorders.ErrInvalid, worker)
		}
		start = ledger.ScheduledStart{Worker: worker, ScheduledBy: actor.ID, ScheduledAt: tx.Now()}
		if usage.ResetsAtMs > tx.Now() {
			start.StartsAt = usage.ResetsAtMs
		}
		if err := tx.Workers().SaveScheduledStart(start); err != nil {
			return err
		}
		crashpoint.Hit("orchestrator.schedule", "recorded")
		return appendStart(tx, ledger.EventOrchestratorStartScheduled, start, actor)
	})
	return start, err
}

// cancelStart ends a start a member scheduled, before it starts anything.
func cancelStart(ctx context.Context, installation ports.Installation, worker string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionRunOrchestrator) {
		return workorders.ErrForbidden
	}
	return installation.Write(ctx, "orchestrator.unschedule", func(tx ports.SharedTx) error {
		gone, err := tx.Workers().DeleteScheduledStart(worker)
		if err != nil {
			return err
		}
		if !gone {
			return fmt.Errorf("worker %s has no scheduled start: %w", worker, ports.ErrNotFound)
		}
		return appendStart(tx, ledger.EventOrchestratorStartEnded, ledger.ScheduledStart{Worker: worker}, actor)
	})
}

// StartScheduled moves every scheduled start a step, and names the workers whose orchestrator it started. A start
// fires once its sign-in's limit is gone, as the member who scheduled it, and does nothing for an orchestrator that
// already runs. Each start is its own write, which reads it again, so ticks that race move it once.
func (w *Workers) StartScheduled(ctx context.Context, actor ledger.Actor) ([]string, error) {
	installation := w.store.Installation(w.scope)
	var starts []ledger.ScheduledStart
	err := installation.Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		starts, err = tx.Workers().ScheduledStarts()
		return err
	})
	if err != nil {
		return nil, err
	}
	var started []string
	var failed []error
	for _, start := range starts {
		fired, err := stepStart(ctx, installation, start.Worker, actor)
		if err != nil {
			failed = append(failed, fmt.Errorf("scheduled start of %s: %w", start.Worker, err))
		}
		if fired {
			started = append(started, start.Worker)
		}
	}
	return started, errors.Join(failed...)
}

func stepStart(ctx context.Context, installation ports.Installation, worker string, actor ledger.Actor) (bool, error) {
	fired := false
	err := installation.Write(ctx, "orchestrator.start_scheduled", func(tx ports.SharedTx) error {
		fired = false
		start, err := tx.Workers().ScheduledStart(worker)
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		record, err := tx.Workers().Find(worker)
		if err != nil {
			return err
		}
		row, err := tx.Workers().Orchestrator(worker)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		// An orchestrator already asked to run ends the start with nothing done, a tripped one included: a breaker that
		// tripped after the start was scheduled waits for a person, and the start never resets it unattended.
		runs := err == nil && row.Desired == ledger.OrchestratorRunning
		step, next := start.Step(signInUsageOf(record.Worker), record.ReportedAt, runs, tx.Now())
		switch step {
		case ledger.StartWaits:
			return nil
		case ledger.StartMoves:
			if err := tx.Workers().SaveScheduledStart(next); err != nil {
				return err
			}
			return appendStart(tx, ledger.EventOrchestratorStartScheduled, next, actor)
		case ledger.StartFires:
			member := ledger.Actor{Kind: ledger.ActorMember, ID: start.ScheduledBy}
			if _, err := setOrchestratorIn(tx, worker, ledger.OrchestratorRunning, member); err != nil {
				return err
			}
			crashpoint.Hit("orchestrator.start_scheduled", "started")
			fired = true
			if keepsFiredStart() {
				return nil
			}
		}
		if _, err := tx.Workers().DeleteScheduledStart(worker); err != nil {
			return err
		}
		return appendStart(tx, ledger.EventOrchestratorStartEnded, next, actor)
	})
	return fired, err
}

// ScheduleOrchestrator records a start of a worker's orchestrator for its sign-in's usage reset.
func (w *Workers) ScheduleOrchestrator(ctx context.Context, worker string, actor ledger.Actor) (ledger.ScheduledStart, error) {
	return scheduleStart(ctx, w.store.Installation(w.scope), worker, actor)
}

// CancelScheduledStart ends a start a member scheduled for a worker's orchestrator.
func (w *Workers) CancelScheduledStart(ctx context.Context, worker string, actor ledger.Actor) error {
	return cancelStart(ctx, w.store.Installation(w.scope), worker, actor)
}

// ScheduleOrchestrator records a start of a worker's orchestrator for its sign-in's usage reset.
func (p *Plane) ScheduleOrchestrator(ctx context.Context, worker string, actor ledger.Actor) (ledger.ScheduledStart, error) {
	return scheduleStart(ctx, p.installation, worker, actor)
}

// CancelScheduledStart ends a start a member scheduled for a worker's orchestrator.
func (p *Plane) CancelScheduledStart(ctx context.Context, worker string, actor ledger.Actor) error {
	return cancelStart(ctx, p.installation, worker, actor)
}

// OrchestratorFacts reads what holds a worker's orchestrator, and the start a member scheduled for it.
func (p *Plane) OrchestratorFacts(ctx context.Context, worker string) (*ledger.OrchestratorHold, *ledger.ScheduledStart, error) {
	var hold *ledger.OrchestratorHold
	var start *ledger.ScheduledStart
	err := p.installation.Read(ctx, func(tx ports.SharedReadTx) error {
		record, err := tx.Workers().Find(worker)
		if err != nil {
			return err
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		hold, start, err = orchestratorFacts(tx.Workers(), record.Worker, p.options.HeartbeatMs, now)
		return err
	})
	return hold, start, err
}

// endScheduledStart ends a worker's scheduled start, if it has one, in the transaction it is given.
func endScheduledStart(tx ports.SharedTx, worker string, actor ledger.Actor) error {
	gone, err := tx.Workers().DeleteScheduledStart(worker)
	if err != nil || !gone {
		return err
	}
	return appendStart(tx, ledger.EventOrchestratorStartEnded, ledger.ScheduledStart{Worker: worker}, actor)
}
