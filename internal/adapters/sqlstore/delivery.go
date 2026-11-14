package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var (
	stmtFoldState = define("delivery.fold_state",
		`SELECT state FROM delivery_fold WHERE workspace_id = :workspace_id`)

	stmtSaveFoldState = define("delivery.save_fold_state",
		`INSERT INTO delivery_fold (workspace_id, state) VALUES (:workspace_id, :state)
ON CONFLICT (workspace_id) DO UPDATE SET state = :state`)

	stmtWIPFull = define("delivery.wip_full",
		`SELECT from_at, to_at FROM delivery_wip_full WHERE workspace_id = :workspace_id ORDER BY from_at`)

	stmtAddWIPFull = define("delivery.add_wip_full",
		`INSERT INTO delivery_wip_full (workspace_id, from_at, to_at) VALUES (:workspace_id, :from_at, :to_at)
ON CONFLICT (workspace_id, from_at) DO UPDATE SET to_at = :to_at`)

	stmtTimeline = define("delivery.timeline",
		`SELECT body FROM work_timelines WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id`)

	stmtSaveTimeline = define("delivery.save_timeline",
		`INSERT INTO work_timelines (workspace_id, work_order_id, version, ended_at, body)
VALUES (:workspace_id, :work_order_id, :version, :ended_at, :body)
ON CONFLICT (workspace_id, work_order_id) DO UPDATE SET version = :version, ended_at = :ended_at, body = :body`)

	stmtRecord = define("delivery.record",
		`SELECT body FROM delivery_records WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id`)

	stmtRecords = define("delivery.records",
		`SELECT body FROM delivery_records WHERE workspace_id = :workspace_id ORDER BY ended_at, work_order_id`)

	stmtSaveRecord = define("delivery.save_record",
		`INSERT INTO delivery_records
  (workspace_id, work_order_id, version, kind, size, capability_class, repository_id, outcome, ended_at, body)
VALUES (:workspace_id, :work_order_id, :version, :kind, :size, :capability_class, :repository_id, :outcome, :ended_at, :body)
ON CONFLICT (workspace_id, work_order_id) DO UPDATE
   SET version = :version, kind = :kind, size = :size, capability_class = :capability_class, repository_id = :repository_id,
       outcome = :outcome, ended_at = :ended_at, body = :body`)

	stmtForgetTimeline = define("delivery.forget_timeline",
		`DELETE FROM work_timelines WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id`)

	stmtForgetRecord = define("delivery.forget_record",
		`DELETE FROM delivery_records WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id`)

	stmtClearTimelines = define("delivery.clear_timelines", `DELETE FROM work_timelines WHERE workspace_id = :workspace_id`)
	stmtClearRecords   = define("delivery.clear_records", `DELETE FROM delivery_records WHERE workspace_id = :workspace_id`)
	stmtClearWIPFull   = define("delivery.clear_wip_full", `DELETE FROM delivery_wip_full WHERE workspace_id = :workspace_id`)
)

// Fold runs fn in one transaction of the delivery fold: a read of the workspace as Read gives it, and writes
// of the rows derived from its log, with no revision and no event, like a renewal.
func (s *Store) Fold(ctx context.Context, scope ledger.WorkspaceID, fn func(ports.FoldTx) error) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	t := &foldTx{readTx: &readTx{session: s.session(ctx, tx, ModeWrite, OpFold), scope: scope, side: s.shared()}}
	defer t.side.rollback()
	if _, err := t.Workspace(); err != nil {
		return err
	}
	if err := fn(t); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: commit: %w", OpFold, err)
	}
	return nil
}

// foldTx is a read of the workspace with its delivery read models beside it.
type foldTx struct {
	*readTx
}

func (t *foldTx) Delivery() ports.DeliveryWriter {
	return deliveryTx{session: t.session, scope: t.scope}
}

// Deliveries reads the delivery read models within a read of the workspace.
func (t *readTx) Deliveries() ports.DeliveryReader {
	return deliveryTx{session: t.session, scope: t.scope}
}

type deliveryTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t deliveryTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

// body reads one JSON row into out, or answers ErrNotFound.
func (t deliveryTx) body(st *statement, p params, what string, out any) error {
	row, err := t.row(st, t.scoped(p))
	if err != nil {
		return err
	}
	var raw string
	if err := row.Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, ports.ErrNotFound)
	} else if err != nil {
		return fmt.Errorf("%s: %w", st.name, err)
	}
	return json.Unmarshal([]byte(raw), out)
}

func (t deliveryTx) FoldState() (delivery.FoldState, error) {
	var out delivery.FoldState
	err := t.body(stmtFoldState, params{}, "the delivery fold's state", &out)
	return out, err
}

func (t deliveryTx) SaveFoldState(state delivery.FoldState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = t.exec(stmtSaveFoldState, t.scoped(params{"state": string(raw)}))
	return err
}

func (t deliveryTx) WIPFull() ([]delivery.Span, error) {
	rows, err := t.rows(stmtWIPFull, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []delivery.Span{}
	for rows.Next() {
		var span delivery.Span
		if err := rows.Scan(&span.From, &span.To); err != nil {
			return nil, fmt.Errorf("%s: %w", stmtWIPFull.name, err)
		}
		out = append(out, span)
	}
	return out, rows.Err()
}

func (t deliveryTx) AddWIPFull(span delivery.Span) error {
	_, err := t.exec(stmtAddWIPFull, t.scoped(params{"from_at": span.From, "to_at": span.To}))
	return err
}

func (t deliveryTx) Timeline(workOrder string) (delivery.Timeline, error) {
	var out delivery.Timeline
	err := t.body(stmtTimeline, params{"work_order_id": workOrder}, "the timeline of "+workOrder, &out)
	return out, err
}

func (t deliveryTx) Record(workOrder string) (delivery.Record, error) {
	var out delivery.Record
	err := t.body(stmtRecord, params{"work_order_id": workOrder}, "the delivery record of "+workOrder, &out)
	return out, err
}

func (t deliveryTx) Records() ([]delivery.Record, error) {
	rows, err := t.rows(stmtRecords, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []delivery.Record{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("%s: %w", stmtRecords.name, err)
		}
		var record delivery.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (t deliveryTx) Save(timeline delivery.Timeline, record *delivery.Record) error {
	raw, err := json.Marshal(timeline)
	if err != nil {
		return err
	}
	p := t.scoped(params{"work_order_id": timeline.WorkOrder, "version": int64(timeline.Version), "ended_at": timeline.End, "body": string(raw)})
	if _, err := t.exec(stmtSaveTimeline, p); err != nil {
		return err
	}
	if record == nil {
		_, err := t.exec(stmtForgetRecord, t.scoped(params{"work_order_id": timeline.WorkOrder}))
		return err
	}
	if raw, err = json.Marshal(record); err != nil {
		return err
	}
	_, err = t.exec(stmtSaveRecord, t.scoped(params{
		"work_order_id": record.WorkOrder, "version": int64(record.Version), "kind": record.Kind, "size": record.Size,
		"capability_class": record.CapabilityClass, "repository_id": record.Repository, "outcome": string(record.Outcome),
		"ended_at": record.Ended, "body": string(raw),
	}))
	return err
}

func (t deliveryTx) Forget(workOrder string) error {
	for _, st := range []*statement{stmtForgetTimeline, stmtForgetRecord} {
		if _, err := t.exec(st, t.scoped(params{"work_order_id": workOrder})); err != nil {
			return err
		}
	}
	return nil
}

func (t deliveryTx) Clear() error {
	for _, st := range []*statement{stmtClearTimelines, stmtClearRecords, stmtClearWIPFull} {
		if _, err := t.exec(st, t.scoped(params{})); err != nil {
			return err
		}
	}
	return nil
}

var _ ports.FoldTx = (*foldTx)(nil)
