package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

const workOrderColumns = `id, kind, state, title, repository_id, priority, size, requirements, needs,
       contract_id, contract_version, fence, claim_id, lease_worker, lease_until,
       takeover_after, branch, resume_lane_id, rework_of, attempts, max_attempts,
       deadline, rev`

var (
	stmtReadWorkOrder = define("work_order.read",
		`SELECT `+workOrderColumns+`
  FROM work_orders
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtListWorkOrders = define("work_order.list",
		`SELECT `+workOrderColumns+`
  FROM work_orders
 WHERE workspace_id = :workspace_id
 ORDER BY id`)

	stmtInsertWorkOrder = define("work_order.insert",
		`INSERT INTO work_orders
  (workspace_id, id, kind, state, title, repository_id, priority, size, requirements, needs,
   contract_id, contract_version, fence, claim_id, lease_worker, lease_until,
   takeover_after, branch, resume_lane_id, rework_of, attempts, max_attempts, deadline, rev)
VALUES
  (:workspace_id, :id, :kind, :state, :title, :repository_id, :priority, :size, :requirements, :needs,
   :contract_id, :contract_version, :fence, :claim_id, :lease_worker, :lease_until,
   :takeover_after, :branch, :resume_lane_id, :rework_of, :attempts, :max_attempts, :deadline, :rev)`)

	stmtDeleteWorkOrders = define("work_order.delete_all",
		`DELETE FROM work_orders WHERE workspace_id = :workspace_id`)

	stmtExpiredLeases = define("work_order.expired",
		`SELECT id FROM work_orders
 WHERE workspace_id = :workspace_id
   AND state IN ('claimed', 'running')
   AND lease_until <= :now
 ORDER BY id`)

	stmtMoveWorkOrder = define("work_order.move",
		`UPDATE work_orders SET state = :to_state
 WHERE workspace_id = :workspace_id AND id = :id AND state = :from_state`)
)

// workOrderTx is the work order area of one transaction.
type workOrderTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t workOrderTx) Get(id string) (workorders.WorkOrder, error) {
	row, err := t.row(stmtReadWorkOrder, params{"workspace_id": string(t.scope), "id": id})
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	w, err := scanWorkOrder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return workorders.WorkOrder{}, fmt.Errorf("work order %s: %w", id, ports.ErrNotFound)
	}
	return w, err
}

func (t workOrderTx) List() ([]workorders.WorkOrder, error) {
	rows, err := t.rows(stmtListWorkOrders, params{"workspace_id": string(t.scope)})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workorders.WorkOrder
	for rows.Next() {
		w, err := scanWorkOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (t workOrderTx) Expired(now int64) ([]string, error) {
	rows, err := t.rows(stmtExpiredLeases, params{"workspace_id": string(t.scope), "now": now})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (t workOrderTx) Move(id string, from, to workorders.State) (bool, error) {
	n, err := t.exec(stmtMoveWorkOrder, params{
		"workspace_id": string(t.scope), "id": id, "from_state": string(from), "to_state": string(to),
	})
	return n > 0, err
}

func (t workOrderTx) Insert(w workorders.WorkOrder) error {
	p, err := workOrderParams(t.scope, w)
	if err != nil {
		return err
	}
	_, err = t.exec(stmtInsertWorkOrder, p)
	return err
}

func (t workOrderTx) ReplaceAll(rows []workorders.WorkOrder) error {
	if _, err := t.exec(stmtDeleteWorkOrders, params{"workspace_id": string(t.scope)}); err != nil {
		return err
	}
	for _, w := range rows {
		if err := t.Insert(w); err != nil {
			return err
		}
	}
	return nil
}

func workOrderParams(scope ledger.WorkspaceID, w workorders.WorkOrder) (params, error) {
	requirements, err := encodeJSON(len(w.Requirements) > 0, w.Requirements)
	if err != nil {
		return nil, err
	}
	needs, err := encodeJSON(len(w.Needs) > 0, w.Needs)
	if err != nil {
		return nil, err
	}
	return params{
		"workspace_id":     string(scope),
		"id":               w.ID,
		"kind":             string(w.Kind),
		"state":            string(w.State),
		"title":            nullText(w.Title),
		"repository_id":    nullText(w.RepositoryID),
		"priority":         w.Priority,
		"size":             nullText(w.Size),
		"requirements":     requirements,
		"needs":            needs,
		"contract_id":      nullText(w.ContractID),
		"contract_version": nullInt(w.ContractVersion),
		"fence":            w.Fence,
		"claim_id":         nullText(w.ClaimID),
		"lease_worker":     nullText(w.LeaseWorker),
		"lease_until":      nullInt(w.LeaseUntil),
		"takeover_after":   nullInt(w.TakeoverAfter),
		"branch":           nullText(w.Branch),
		"resume_lane_id":   nullText(w.ResumeLaneID),
		"rework_of":        nullText(w.ReworkOf),
		"attempts":         w.Attempts,
		"max_attempts":     w.MaxAttempts,
		"deadline":         nullInt(w.Deadline),
		"rev":              w.Rev,
	}, nil
}

func encodeJSON(present bool, value any) (sql.NullString, error) {
	if !present {
		return sql.NullString{}, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(raw), Valid: true}, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanWorkOrder(row scanner) (workorders.WorkOrder, error) {
	var w workorders.WorkOrder
	var kind, state string
	var title, repository, size, requirements, needs, contractID, claimID, leaseWorker sql.NullString
	var branch, resumeLane, reworkOf sql.NullString
	var contractVersion, leaseUntil, takeoverAfter, deadline sql.NullInt64
	err := row.Scan(&w.ID, &kind, &state, &title, &repository, &w.Priority, &size, &requirements, &needs,
		&contractID, &contractVersion, &w.Fence, &claimID, &leaseWorker, &leaseUntil,
		&takeoverAfter, &branch, &resumeLane, &reworkOf, &w.Attempts, &w.MaxAttempts,
		&deadline, &w.Rev)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	w.Kind, w.State = workorders.Kind(kind), workorders.State(state)
	w.Title, w.RepositoryID, w.Size = title.String, repository.String, size.String
	w.ContractID, w.ContractVersion = contractID.String, contractVersion.Int64
	w.ClaimID, w.LeaseWorker, w.LeaseUntil = claimID.String, leaseWorker.String, leaseUntil.Int64
	w.TakeoverAfter, w.Branch, w.ResumeLaneID = takeoverAfter.Int64, branch.String, resumeLane.String
	w.ReworkOf, w.Deadline = reworkOf.String, deadline.Int64
	if requirements.Valid {
		if err := json.Unmarshal([]byte(requirements.String), &w.Requirements); err != nil {
			return workorders.WorkOrder{}, fmt.Errorf("requirements: %w", err)
		}
	}
	if needs.Valid {
		if err := json.Unmarshal([]byte(needs.String), &w.Needs); err != nil {
			return workorders.WorkOrder{}, fmt.Errorf("needs: %w", err)
		}
	}
	return w, nil
}
