package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var (
	stmtLatestForecasts = define("forecast.latest",
		`SELECT body FROM forecasts f
 WHERE workspace_id = :workspace_id AND plan_id = :plan_id AND plan_version = :plan_version
   AND day = (SELECT MAX(day) FROM forecasts g WHERE g.workspace_id = f.workspace_id AND g.plan_id = f.plan_id
     AND g.plan_version = f.plan_version AND g.milestone_id = f.milestone_id)
 ORDER BY milestone_id`)

	stmtForecastHistory = define("forecast.history",
		`SELECT body FROM forecasts
 WHERE workspace_id = :workspace_id AND plan_id = :plan_id AND plan_version = :plan_version AND milestone_id = :milestone_id
 ORDER BY day`)

	stmtDropForecastDay = define("forecast.drop_day",
		`DELETE FROM forecasts
 WHERE workspace_id = :workspace_id AND plan_id = :plan_id AND plan_version = :plan_version AND milestone_id = :milestone_id AND day = :day`)

	stmtAddForecast = define("forecast.add",
		`INSERT INTO forecasts (workspace_id, id, plan_id, plan_version, milestone_id, estimate_set_id, status, ledger_rev, day, created_at, body)
VALUES (:workspace_id, :id, :plan_id, :plan_version, :milestone_id, :estimate_set_id, :status, :ledger_rev, :day, :created_at, :body)`)
)

// Forecast runs fn in one transaction of the forecast tick: a read of the workspace as Read gives it, and writes of
// the forecasts and estimate sets derived from its log, with no revision and no event, as the fold's.
func (s *Store) Forecast(ctx context.Context, scope ledger.WorkspaceID, fn func(ports.ForecastTx) error) error {
	return s.derive(ctx, scope, OpForecast, func(t *readTx) error { return fn(&forecastTickTx{readTx: t}) })
}

// forecastTickTx is a read of the workspace with the estimate sets and forecasts beside it, and no delivery read
// model.
type forecastTickTx struct {
	*readTx
}

func (t *forecastTickTx) EstimateSets() ports.EstimateWriter {
	return estimateTx{session: t.session, scope: t.scope}
}

func (t *forecastTickTx) Forecast() ports.ForecastWriter {
	return forecastTx{session: t.session, scope: t.scope}
}

var _ ports.ForecastTx = (*forecastTickTx)(nil)

// Forecasts reads kept forecasts within a read of the workspace.
func (t *readTx) Forecasts() ports.ForecastReader {
	return forecastTx{session: t.session, scope: t.scope}
}

type forecastTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t forecastTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

// list reads the forecasts a statement selects, each from its JSON body.
func (t forecastTx) list(st *statement, p params) ([]delivery.Forecast, error) {
	rows, err := t.rows(st, t.scoped(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []delivery.Forecast{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("%s: %w", st.name, err)
		}
		var f delivery.Forecast
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			return nil, fmt.Errorf("%s: %w", st.name, err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (t forecastTx) Latest(plan string, version int64) ([]delivery.Forecast, error) {
	return t.list(stmtLatestForecasts, params{"plan_id": plan, "plan_version": version})
}

func (t forecastTx) History(plan string, version int64, milestone string) ([]delivery.Forecast, error) {
	return t.list(stmtForecastHistory, params{"plan_id": plan, "plan_version": version, "milestone_id": milestone})
}

func (t forecastTx) Keep(forecasts []delivery.Forecast) error {
	for _, f := range forecasts {
		where := params{"plan_id": f.Plan, "plan_version": f.PlanVersion, "milestone_id": f.Milestone, "day": f.Day}
		if _, err := t.exec(stmtDropForecastDay, t.scoped(where)); err != nil {
			return err
		}
		raw, err := json.Marshal(f)
		if err != nil {
			return err
		}
		_, err = t.exec(stmtAddForecast, t.scoped(params{
			"id": f.ID, "plan_id": f.Plan, "plan_version": f.PlanVersion, "milestone_id": f.Milestone, "estimate_set_id": f.SetID,
			"status": string(f.Status), "ledger_rev": f.Rev, "day": f.Day, "created_at": f.CreatedAt, "body": string(raw),
		}))
		if err != nil {
			return err
		}
	}
	return nil
}

var _ ports.ForecastWriter = forecastTx{}
