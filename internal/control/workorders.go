package control

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
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
		if err := refusePlannedAdoption(tx.Approvals(), record); err != nil {
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

// refusePlannedAdoption refuses work that adopts a project once a member approved a plan of it: the project
// then plans its next work with a planning session, which starts from the plan it has (ADR-0023). A plan
// that only waits for approval is not one the project has yet.
func refusePlannedAdoption(approvals ports.ApprovalReader, record workorders.WorkOrder) error {
	if !record.Adopt {
		return nil
	}
	rows, err := approvals.All()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Kind == authority.KindPlan && row.State == authority.ApprovalApproved {
			return fmt.Errorf("%w (%s), so it plans its next work with a planning session: run 'ropeshift plan start'", workorders.ErrAlreadyPlanned, row.Summary)
		}
	}
	return nil
}

// LedgerWork is a work order that lives only in the ledger, with the contract it
// pins (FR-LEDGER-3).
type LedgerWork struct {
	Order     workorders.WorkOrder
	Statement string
	Criteria  []planning.Criterion
	DependsOn []string
}

// Record creates ledger work in one transaction and carries it to
// `contract_review`, where a person reviews it as they would a contract file. The
// client generates the work order's ID, so a retry returns what the first attempt
// wrote.
func (w *WorkOrders) Record(ctx context.Context, scope ledger.WorkspaceID, work LedgerWork, actor ledger.Actor) (workorders.WorkOrder, error) {
	record, err := workorders.New(work.Order)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	if record.Title == "" {
		return workorders.WorkOrder{}, fmt.Errorf("%w: no title", workorders.ErrInvalid)
	}
	if record.Kind == workorders.KindContractReview {
		return workorders.WorkOrder{}, fmt.Errorf("%w: a review reads a plan, so start one with plan review", workorders.ErrInvalid)
	}
	if record.Size, err = planning.CheckSize(record.Size); err != nil {
		return workorders.WorkOrder{}, fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	err = w.store.Write(ctx, scope, "work_order.record", func(tx ports.WriteTx) error {
		existing, err := tx.WorkOrders().Get(record.ID)
		if err == nil {
			record = existing
			return nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		if err := refusePlannedAdoption(tx.Approvals(), record); err != nil {
			return err
		}
		pinned, err := recordLedgerWork(tx, record, work, actor)
		record = pinned
		return err
	})
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	return record, nil
}

func recordLedgerWork(tx ports.WriteTx, record workorders.WorkOrder, work LedgerWork, actor ledger.Actor) (workorders.WorkOrder, error) {
	r := planningWriter{tx: tx, actor: actor}
	planID, err := newID(tx.Now())
	if err != nil {
		return record, err
	}
	contractID, err := newID(tx.Now())
	if err != nil {
		return record, err
	}
	contract := planning.Contract{
		ID: contractID, Version: 1, PlanID: planID, Title: record.Title, Statement: work.Statement,
		Criteria: work.Criteria, Requirements: record.Requirements, Status: planning.StatusActive,
	}
	if contract.Digest, err = planning.LedgerDigest(contract); err != nil {
		return record, err
	}
	plan := planning.Plan{ID: planID, Title: record.Title, Status: planning.StatusActive}
	if err := r.plan(plan); err != nil {
		return record, err
	}
	if err := r.contract(contract); err != nil {
		return record, err
	}
	record.ContractID, record.ContractVersion = contract.ID, contract.Version
	if err := tx.WorkOrders().Insert(record); err != nil {
		return record, err
	}
	event, err := workorders.CreatedEvent(record, actor)
	if err != nil {
		return record, err
	}
	if _, err := tx.Append(event); err != nil {
		return record, err
	}
	for _, dependsOn := range work.DependsOn {
		if err := r.dependency(planning.Dependency{WorkOrder: record.ID, DependsOn: dependsOn, Kind: planning.DependencyBlocking}); err != nil {
			return record, err
		}
	}
	for _, trigger := range []workorders.Trigger{workorders.TriggerAccepted, workorders.TriggerContractSubmitted} {
		if err := takeEdge(tx, record.ID, trigger, actor); err != nil {
			return record, err
		}
	}
	return tx.WorkOrders().Get(record.ID)
}

// Move takes one unleased edge of the state machine. The compare-and-set repeats
// the state read, so a racing move loses instead of skipping an edge. The edges out of
// `acceptance` answer to the evidence, so only Acceptances takes them, and a cancel ends lanes and
// answers to a member, so only Leases takes that.
func (w *WorkOrders) Move(ctx context.Context, scope ledger.WorkspaceID, id string, trigger workorders.Trigger, actor ledger.Actor) error {
	if workorders.LeavesAcceptance(trigger) || trigger == workorders.TriggerWakeFired || trigger == workorders.TriggerCanceled {
		return workorders.ErrForbidden
	}
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
