package control

import (
	"context"
	"errors"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// EventProjectionRebuilt records that a projection was rebuilt from the log.
const EventProjectionRebuilt = "ledger.projection_rebuilt"

// WorkOrders holds the use cases that create work orders and maintain their
// projection.
type WorkOrders struct {
	store ports.Store
}

// NewWorkOrders returns the use cases over a Store.
func NewWorkOrders(store ports.Store) *WorkOrders {
	return &WorkOrders{store: store}
}

// Create records a new work order in `intake`. A retry with the same ID returns
// the record the first attempt wrote and appends nothing.
func (w *WorkOrders) Create(ctx context.Context, scope ledger.WorkspaceID, draft workorders.WorkOrder, actor ledger.Actor) (workorders.WorkOrder, error) {
	record, err := workorders.New(draft)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	err = w.store.Write(ctx, scope, "work_order.create", func(tx ports.WriteTx) error {
		existing, err := tx.WorkOrders().Get(record.ID)
		if err == nil {
			record = existing
			return nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		if err := tx.WorkOrders().Insert(record); err != nil {
			return err
		}
		event, err := workorders.CreatedEvent(record, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	return record, nil
}

// Move takes one unleased edge of the state machine. The compare-and-set repeats
// the state read, so a racing move loses instead of skipping an edge.
func (w *WorkOrders) Move(ctx context.Context, scope ledger.WorkspaceID, id string, trigger workorders.Trigger, actor ledger.Actor) error {
	return w.store.Write(ctx, scope, "work_order.move", func(tx ports.WriteTx) error {
		return takeEdge(tx, id, trigger, actor)
	})
}

// takeEdge takes one unleased edge inside a transaction.
func takeEdge(tx ports.WriteTx, id string, trigger workorders.Trigger, actor ledger.Actor) error {
	current, err := tx.WorkOrders().Get(id)
	if err != nil {
		return err
	}
	edge, err := workorders.Move(current, trigger)
	if err != nil {
		return err
	}
	won, err := tx.WorkOrders().Move(id, edge.From, edge.To)
	if err != nil {
		return err
	}
	if !won {
		return workorders.ErrIllegalMove
	}
	event, err := workorders.ChangeEvent(workorders.EventType(trigger), id, workorders.Change{Trigger: trigger}, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// Rebuild replaces the work order projection with what the event log replays to.
// Renewal appends no event, so a lease deadline is not in the log: a row whose
// claim still holds keeps the deadline it has.
func (w *WorkOrders) Rebuild(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) error {
	return w.store.Write(ctx, scope, "projection.rebuild", func(tx ports.WriteTx) error {
		events, err := ReadAllEvents(tx.Events)
		if err != nil {
			return err
		}
		book, err := workorders.Replay(events)
		if err != nil {
			return err
		}
		current, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range current {
			replayed, ok := book[row.ID]
			if ok && replayed.ClaimID == row.ClaimID && replayed.Fence == row.Fence && row.LeaseUntil > replayed.LeaseUntil {
				replayed.LeaseUntil = row.LeaseUntil
				book[row.ID] = replayed
			}
		}
		rows := make([]workorders.WorkOrder, 0, len(book))
		for _, row := range book {
			rows = append(rows, row)
		}
		if err := tx.WorkOrders().ReplaceAll(rows); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type:    EventProjectionRebuilt,
			Actor:   actor,
			Subject: ledger.Subject{Kind: "projection", ID: workorders.SubjectKind},
		})
		return err
	})
}

const eventPage = 1000

// ReadAllEvents pages through the log from the start.
func ReadAllEvents(read func(after int64, limit int) ([]ledger.Event, error)) ([]ledger.Event, error) {
	var all []ledger.Event
	var after int64
	for {
		page, err := read(after, eventPage)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < eventPage {
			return all, nil
		}
		after = page[len(page)-1].Rev
	}
}
