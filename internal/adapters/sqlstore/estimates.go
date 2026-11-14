package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var (
	stmtEstimateSet = define("estimate.set",
		`SELECT plan_id, plan_version, ledger_rev, estimator_version, policy_version, digest, created_at
  FROM estimate_sets WHERE workspace_id = :workspace_id AND id = :id`)

	stmtEstimatesOfSet = define("estimate.of_set",
		`SELECT current, body FROM estimates WHERE workspace_id = :workspace_id AND estimate_set_id = :estimate_set_id
 ORDER BY work_order_id`)

	stmtCurrentEstimate = define("estimate.current",
		`SELECT current, body FROM estimates WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id AND current <> 0`)

	stmtAddEstimateSet = define("estimate.add_set",
		`INSERT INTO estimate_sets (workspace_id, id, plan_id, plan_version, ledger_rev, estimator_version, policy_version, digest, created_at)
VALUES (:workspace_id, :id, :plan_id, :plan_version, :ledger_rev, :estimator_version, :policy_version, :digest, :created_at)`)

	stmtReplaceCurrentEstimate = define("estimate.replace_current",
		`UPDATE estimates SET current = 0 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id AND current <> 0`)

	stmtAddEstimate = define("estimate.add",
		`INSERT INTO estimates (workspace_id, id, estimate_set_id, work_order_id, status, confidence, current, created_at, body)
VALUES (:workspace_id, :id, :estimate_set_id, :work_order_id, :status, :confidence, 1, :created_at, :body)`)
)

// Estimates reads estimate sets within a read of the workspace.
func (t *readTx) Estimates() ports.EstimateReader {
	return estimateTx{session: t.session, scope: t.scope}
}

// Deliveries reads the delivery read models within a write, for the estimates it makes.
func (t *writeTx) Deliveries() ports.DeliveryReader {
	return deliveryTx{session: t.session, scope: t.scope}
}

// EstimateSets keeps estimate sets within a transaction of the fold.
func (t *foldTx) EstimateSets() ports.EstimateWriter {
	return estimateTx{session: t.session, scope: t.scope}
}

// Estimates reads and keeps estimate sets within a write.
func (t *writeTx) Estimates() ports.EstimateWriter {
	return estimateTx{session: t.session, scope: t.scope}
}

type estimateTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t estimateTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

// estimateOf reads one estimate row: its body, and the flag that says it is still current.
func estimateOf(st *statement, scan func(...any) error) (delivery.Estimate, error) {
	var current int64
	var raw string
	if err := scan(&current, &raw); err != nil {
		return delivery.Estimate{}, err
	}
	var out delivery.Estimate
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return delivery.Estimate{}, fmt.Errorf("%s: %w", st.name, err)
	}
	out.Current = current != 0
	return out, nil
}

func (t estimateTx) Set(id string) (delivery.EstimateSet, error) {
	row, err := t.row(stmtEstimateSet, t.scoped(params{"id": id}))
	if err != nil {
		return delivery.EstimateSet{}, err
	}
	out := delivery.EstimateSet{ID: id}
	var estimator int64
	err = row.Scan(&out.Plan, &out.PlanVersion, &out.Rev, &estimator, &out.PolicyVersion, &out.Digest, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return delivery.EstimateSet{}, fmt.Errorf("estimate set %s: %w", id, ports.ErrNotFound)
	}
	if err != nil {
		return delivery.EstimateSet{}, fmt.Errorf("%s: %w", stmtEstimateSet.name, err)
	}
	out.EstimatorVersion = int(estimator)
	rows, err := t.rows(stmtEstimatesOfSet, t.scoped(params{"estimate_set_id": id}))
	if err != nil {
		return delivery.EstimateSet{}, err
	}
	defer rows.Close()
	out.Estimates = []delivery.Estimate{}
	for rows.Next() {
		e, err := estimateOf(stmtEstimatesOfSet, rows.Scan)
		if err != nil {
			return delivery.EstimateSet{}, err
		}
		out.Estimates = append(out.Estimates, e)
	}
	return out, rows.Err()
}

func (t estimateTx) Current(workOrder string) (delivery.Estimate, error) {
	row, err := t.row(stmtCurrentEstimate, t.scoped(params{"work_order_id": workOrder}))
	if err != nil {
		return delivery.Estimate{}, err
	}
	e, err := estimateOf(stmtCurrentEstimate, row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return delivery.Estimate{}, fmt.Errorf("the current estimate of %s: %w", workOrder, ports.ErrNotFound)
	}
	return e, err
}

func (t estimateTx) Add(set delivery.EstimateSet) error {
	_, err := t.exec(stmtAddEstimateSet, t.scoped(params{
		"id": set.ID, "plan_id": set.Plan, "plan_version": set.PlanVersion, "ledger_rev": set.Rev,
		"estimator_version": int64(set.EstimatorVersion), "policy_version": set.PolicyVersion, "digest": set.Digest, "created_at": set.CreatedAt,
	}))
	if err != nil {
		return err
	}
	for _, e := range set.Estimates {
		if _, err := t.exec(stmtReplaceCurrentEstimate, t.scoped(params{"work_order_id": e.WorkOrder})); err != nil {
			return err
		}
		// The flag lives in its column, so the body says what the estimate said when it was made.
		kept := e
		kept.Current = false
		raw, err := json.Marshal(kept)
		if err != nil {
			return err
		}
		_, err = t.exec(stmtAddEstimate, t.scoped(params{
			"id": e.ID, "estimate_set_id": set.ID, "work_order_id": e.WorkOrder, "status": string(e.Status),
			"confidence": string(e.Confidence), "created_at": set.CreatedAt, "body": string(raw),
		}))
		if err != nil {
			return err
		}
	}
	return nil
}

var _ ports.EstimateWriter = estimateTx{}
