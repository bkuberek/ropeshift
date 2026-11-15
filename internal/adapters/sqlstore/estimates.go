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

	stmtNewestEstimateSet = define("estimate.newest_set",
		`SELECT id FROM estimate_sets WHERE workspace_id = :workspace_id AND plan_id = :plan_id AND plan_version = :plan_version
 ORDER BY created_at DESC, ledger_rev DESC, id DESC LIMIT 1`)

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

	stmtBaselineOf = define("estimate.baseline_of",
		`SELECT b.body, e.current, e.body FROM estimate_baselines b
  JOIN estimates e ON e.workspace_id = b.workspace_id AND e.estimate_set_id = b.estimate_set_id
 WHERE b.workspace_id = :workspace_id AND b.estimate_set_id <> '' AND e.work_order_id = :work_order_id
 ORDER BY b.ledger_rev, b.created_at, b.id LIMIT 1`)

	stmtBaselinesOfPlan = define("estimate.baselines_of_plan",
		`SELECT body FROM estimate_baselines WHERE workspace_id = :workspace_id AND plan_id = :plan_id
 ORDER BY ledger_rev, created_at, id`)

	stmtAddBaseline = define("estimate.add_baseline",
		`INSERT INTO estimate_baselines (workspace_id, id, approval_id, plan_id, plan_version, estimate_set_id, set_digest, ledger_rev, created_at, body)
VALUES (:workspace_id, :id, :approval_id, :plan_id, :plan_version, :estimate_set_id, :set_digest, :ledger_rev, :created_at, :body)`)
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

func (t estimateTx) NewestOf(plan string, version int64) (delivery.EstimateSet, error) {
	row, err := t.row(stmtNewestEstimateSet, t.scoped(params{"plan_id": plan, "plan_version": version}))
	if err != nil {
		return delivery.EstimateSet{}, err
	}
	var id string
	err = row.Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return delivery.EstimateSet{}, fmt.Errorf("an estimate set of plan %s version %d: %w", plan, version, ports.ErrNotFound)
	}
	if err != nil {
		return delivery.EstimateSet{}, fmt.Errorf("%s: %w", stmtNewestEstimateSet.name, err)
	}
	return t.Set(id)
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

func (t estimateTx) BaselineOf(workOrder string) (delivery.Baseline, delivery.Estimate, error) {
	row, err := t.row(stmtBaselineOf, t.scoped(params{"work_order_id": workOrder}))
	if err != nil {
		return delivery.Baseline{}, delivery.Estimate{}, err
	}
	var raw string
	var baseline delivery.Baseline
	e, err := estimateOf(stmtBaselineOf, func(dest ...any) error { return row.Scan(append([]any{&raw}, dest...)...) })
	if errors.Is(err, sql.ErrNoRows) {
		return delivery.Baseline{}, delivery.Estimate{}, fmt.Errorf("the baseline of %s: %w", workOrder, ports.ErrNotFound)
	}
	if err != nil {
		return delivery.Baseline{}, delivery.Estimate{}, err
	}
	if err := json.Unmarshal([]byte(raw), &baseline); err != nil {
		return delivery.Baseline{}, delivery.Estimate{}, fmt.Errorf("%s: %w", stmtBaselineOf.name, err)
	}
	return baseline, e, nil
}

func (t estimateTx) Baselines(plan string) ([]delivery.Baseline, error) {
	rows, err := t.rows(stmtBaselinesOfPlan, t.scoped(params{"plan_id": plan}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []delivery.Baseline{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("%s: %w", stmtBaselinesOfPlan.name, err)
		}
		var baseline delivery.Baseline
		if err := json.Unmarshal([]byte(raw), &baseline); err != nil {
			return nil, fmt.Errorf("%s: %w", stmtBaselinesOfPlan.name, err)
		}
		out = append(out, baseline)
	}
	return out, rows.Err()
}

func (t estimateTx) AddBaseline(baseline delivery.Baseline) error {
	if baseline.Forecasts == nil {
		baseline.Forecasts = []delivery.Forecast{}
	}
	raw, err := json.Marshal(baseline)
	if err != nil {
		return err
	}
	_, err = t.exec(stmtAddBaseline, t.scoped(params{
		"id": baseline.ID, "approval_id": baseline.Approval, "plan_id": baseline.Plan, "plan_version": baseline.PlanVersion,
		"estimate_set_id": baseline.SetID, "set_digest": baseline.SetDigest, "ledger_rev": baseline.Rev, "created_at": baseline.CreatedAt, "body": string(raw),
	}))
	return err
}

var _ ports.EstimateWriter = estimateTx{}
