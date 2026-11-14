package control

import (
	"context"
	"encoding/json"
	"errors"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// foldPage is how many events one transaction of the fold reads, so a backfill of a long log is many short
// transactions and none holds the database for long.
const foldPage = 500

// DeliveryFold keeps a project's delivery read models current from its log (ADR-0026): each finished work
// order's timeline, each finished change's or rework's delivery record, and the spans in which the
// project's work-in-progress cap was full. It reads the log from a cursor it keeps, so it reads each event
// once, and a first run reads the whole log from the start, which backfills every work order that ended
// before it. A new derivation version drops every row and folds the log again (FR-EST-3).
type DeliveryFold struct {
	store ports.Store
}

// NewDeliveryFold returns the fold of a store.
func NewDeliveryFold(store ports.Store) *DeliveryFold { return &DeliveryFold{store: store} }

// Fold reads every event past the cursor, a page per transaction, and returns how many it read.
func (d *DeliveryFold) Fold(ctx context.Context, scope ledger.WorkspaceID) (int, error) {
	total := 0
	for {
		read, err := d.page(ctx, scope)
		total += read
		if err != nil || read < foldPage {
			return total, err
		}
	}
}

// page folds one page of events in one transaction.
func (d *DeliveryFold) page(ctx context.Context, scope ledger.WorkspaceID) (int, error) {
	read := 0
	err := d.store.Fold(ctx, scope, func(tx ports.FoldTx) error {
		rows := tx.Delivery()
		now, err := tx.Now()
		if err != nil {
			return err
		}
		state, err := d.state(tx, rows, now)
		if err != nil {
			return err
		}
		events, err := tx.Events(state.Rev, foldPage)
		if err != nil {
			return err
		}
		for _, e := range events {
			if err := d.event(tx, rows, &state, e, now); err != nil {
				return err
			}
			state.Rev = e.Rev
			read++
		}
		return rows.SaveFoldState(state)
	})
	return read, err
}

// state reads the fold's state, and starts it over from the start of the log on its first run and on a new
// derivation version. The work-in-progress cap starts at the one in force now, until the log says it
// changed; a first run's time stays, because capacity intervals were recorded only from about then.
func (d *DeliveryFold) state(tx ports.FoldTx, rows ports.DeliveryWriter, now int64) (delivery.FoldState, error) {
	state, err := rows.FoldState()
	switch {
	case err == nil && state.Version == delivery.Version:
		return state, nil
	case err == nil:
		if err := rows.Clear(); err != nil {
			return delivery.FoldState{}, err
		}
	case errors.Is(err, ports.ErrNotFound):
		state.FirstRunAt = now
	default:
		return delivery.FoldState{}, err
	}
	workspace, err := tx.Workspace()
	if err != nil {
		return delivery.FoldState{}, err
	}
	return delivery.FoldState{Version: delivery.Version, FirstRunAt: state.FirstRunAt, WIPLimit: workspace.WIPLimit, InProgress: []string{}}, nil
}

// event folds one event: a change of the work-in-progress cap, or a work order's move, which counts toward
// the work in progress and, when it ends the work order, derives and keeps its timeline and record.
func (d *DeliveryFold) event(tx ports.FoldTx, rows ports.DeliveryWriter, state *delivery.FoldState, e ledger.Event, now int64) error {
	if e.Type == eventFleetChanged {
		var fleet struct {
			WIPLimit int64 `json:"wip_limit"`
		}
		if json.Unmarshal(e.Data, &fleet) != nil {
			return nil
		}
		if span, ended := state.Limit(fleet.WIPLimit, e.RecordedAt); ended {
			return rows.AddWIPFull(span)
		}
		return nil
	}
	if e.Subject.Kind != workorders.SubjectKind {
		return nil
	}
	var change workorders.Change
	if json.Unmarshal(e.Data, &change) != nil || change.Trigger == "" {
		return nil
	}
	to, known := delivery.Target(change.Trigger)
	if !known {
		return nil
	}
	if span, ended := state.Move(e.Subject.ID, to, e.RecordedAt); ended {
		if err := rows.AddWIPFull(span); err != nil {
			return err
		}
	}
	switch {
	case workorders.Terminal(to):
		return d.ended(tx, rows, *state, e.Subject.ID, now)
	case change.Trigger == workorders.TriggerRetried:
		return rows.Forget(e.Subject.ID)
	}
	return nil
}

// ended derives and keeps a work order that reached a terminal state, and, for a review, the work it
// reviews, whose review lanes count in its record.
func (d *DeliveryFold) ended(tx ports.FoldTx, rows ports.DeliveryWriter, state delivery.FoldState, id string, now int64) error {
	if err := d.keep(tx, rows, state, id, now); err != nil {
		return err
	}
	order, err := tx.WorkOrders().Get(id)
	if err != nil {
		return err
	}
	if order.Reviews == "" {
		return nil
	}
	return d.keep(tx, rows, state, order.Reviews, now)
}

// keep derives one work order as the store holds it now, and keeps its timeline and record while it stands
// in a terminal state. One that moved on since, as a retry of failed work does, keeps nothing until it ends
// again.
func (d *DeliveryFold) keep(tx ports.FoldTx, rows ports.DeliveryWriter, state delivery.FoldState, id string, now int64) error {
	full, err := rows.WIPFull()
	if err != nil {
		return err
	}
	timeline, record, err := DeliveryOf(tx, id, DeliveryOptions{Now: now, WIPFull: append(full, state.Open(now)...), CapacitySince: state.FirstRunAt})
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if timeline.Terminal == "" {
		return rows.Forget(id)
	}
	return rows.Save(timeline, record)
}
