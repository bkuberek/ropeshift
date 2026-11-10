package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

const workOrderColumns = `id, kind, state, title, repository_id, priority, size, requirements, needs,
       contract_id, contract_version, fence, claim_id, lease_worker, lease_until,
       takeover_after, branch, resume_lane_id, rework_of, attempts, max_attempts,
       deadline, reviewed_by, rev, reviews_plan, review_pins, plan_commit, base`

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

	// stmtWoken reads work a wake made ready, the highest priority first.
	stmtWoken = define("work_order.woken",
		`SELECT `+workOrderColumns+`
  FROM work_orders
 WHERE workspace_id = :workspace_id
   AND state = 'ready'
   AND resume_lane_id IS NOT NULL
 ORDER BY priority DESC, id`)

	stmtHeldBy = define("work_order.held_by",
		`SELECT `+workOrderColumns+`
  FROM work_orders
 WHERE workspace_id = :workspace_id
   AND lease_worker = :worker
 ORDER BY id`)

	stmtWorkOrdersByContract = define("work_order.by_contract",
		`SELECT `+workOrderColumns+`
  FROM work_orders
 WHERE workspace_id = :workspace_id
   AND contract_id = :contract_id
 ORDER BY id`)

	stmtInsertWorkOrder = define("work_order.insert",
		`INSERT INTO work_orders
  (workspace_id, id, kind, state, title, repository_id, priority, size, requirements, needs,
   contract_id, contract_version, fence, claim_id, lease_worker, lease_until,
   takeover_after, branch, resume_lane_id, rework_of, attempts, max_attempts, deadline, reviewed_by, rev, reviews_plan, review_pins, plan_commit, base)
VALUES
  (:workspace_id, :id, :kind, :state, :title, :repository_id, :priority, :size, :requirements, :needs,
   :contract_id, :contract_version, :fence, :claim_id, :lease_worker, :lease_until,
   :takeover_after, :branch, :resume_lane_id, :rework_of, :attempts, :max_attempts, :deadline, :reviewed_by, :rev, :reviews_plan, :review_pins, :plan_commit, :base)`)

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

	stmtReviewedWorkOrder = define("work_order.reviewed",
		`UPDATE work_orders SET reviewed_by = :member
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtTakenInWorkOrder = define("work_order.taken_in",
		`UPDATE work_orders SET plan_commit = :commit
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtPinWorkOrder = define("work_order.pin",
		`UPDATE work_orders SET contract_version = :version, reviewed_by = NULL
 WHERE workspace_id = :workspace_id AND id = :id`)
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

func (t workOrderTx) list(st *statement, p params) ([]workorders.WorkOrder, error) {
	p["workspace_id"] = string(t.scope)
	rows, err := t.rows(st, p)
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

func (t workOrderTx) List() ([]workorders.WorkOrder, error) {
	return t.list(stmtListWorkOrders, params{})
}

func (t workOrderTx) HeldBy(worker string) ([]workorders.WorkOrder, error) {
	return t.list(stmtHeldBy, params{"worker": worker})
}

func (t workOrderTx) Woken() ([]workorders.WorkOrder, error) {
	return t.list(stmtWoken, params{})
}

func (t workOrderTx) ByContract(contract string) ([]workorders.WorkOrder, error) {
	return t.list(stmtWorkOrdersByContract, params{"contract_id": contract})
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

func (t workOrderTx) Reviewed(id, member string) error {
	_, err := t.exec(stmtReviewedWorkOrder, params{
		"workspace_id": string(t.scope), "id": id, "member": nullText(member),
	})
	return err
}

func (t workOrderTx) TakenIn(id, commit string) error {
	_, err := t.exec(stmtTakenInWorkOrder, params{"workspace_id": string(t.scope), "id": id, "commit": commit})
	return err
}

func (t workOrderTx) Pin(id string, version int64) error {
	_, err := t.exec(stmtPinWorkOrder, params{
		"workspace_id": string(t.scope), "id": id, "version": version,
	})
	return err
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
	pins, err := encodeJSON(len(w.ReviewPins) > 0, w.ReviewPins)
	if err != nil {
		return nil, err
	}
	return params{
		"reviews_plan":     nullText(w.ReviewsPlan),
		"review_pins":      pins,
		"plan_commit":      nullText(w.PlanCommit),
		"base":             nullText(w.Base),
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
		"reviewed_by":      nullText(w.ReviewedBy),
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
	var branch, resumeLane, reworkOf, reviewedBy, reviewsPlan, reviewPins, planCommit, base sql.NullString
	var contractVersion, leaseUntil, takeoverAfter, deadline sql.NullInt64
	err := row.Scan(&w.ID, &kind, &state, &title, &repository, &w.Priority, &size, &requirements, &needs,
		&contractID, &contractVersion, &w.Fence, &claimID, &leaseWorker, &leaseUntil,
		&takeoverAfter, &branch, &resumeLane, &reworkOf, &w.Attempts, &w.MaxAttempts,
		&deadline, &reviewedBy, &w.Rev, &reviewsPlan, &reviewPins, &planCommit, &base)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	w.ReviewsPlan, w.PlanCommit, w.Base = reviewsPlan.String, planCommit.String, base.String
	if reviewPins.Valid {
		if err := json.Unmarshal([]byte(reviewPins.String), &w.ReviewPins); err != nil {
			return workorders.WorkOrder{}, fmt.Errorf("review pins: %w", err)
		}
	}
	w.Kind, w.State = workorders.Kind(kind), workorders.State(state)
	w.Title, w.RepositoryID, w.Size = title.String, repository.String, size.String
	w.ContractID, w.ContractVersion = contractID.String, contractVersion.Int64
	w.ClaimID, w.LeaseWorker, w.LeaseUntil = claimID.String, leaseWorker.String, leaseUntil.Int64
	w.TakeoverAfter, w.Branch, w.ResumeLaneID = takeoverAfter.Int64, branch.String, resumeLane.String
	w.ReworkOf, w.Deadline, w.ReviewedBy = reworkOf.String, deadline.Int64, reviewedBy.String
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
