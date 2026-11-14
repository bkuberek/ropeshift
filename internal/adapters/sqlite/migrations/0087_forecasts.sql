-- Each forecast the forecast tick kept (ADR-0026, FR-EST-8): a plan version's own, with an empty milestone_id, and
-- each of its milestones', as JSON with the explanation it showed (FR-EST-14). estimate_set_id names the set of
-- estimates it was made from. day is the UTC midnight of when it was kept, and a plan version keeps one forecast
-- per milestone and day, the last of that day, so the newest is the latest and the rest show how it moved. A
-- forecast is derived from the log, so it never changes and takes no revision.
CREATE TABLE forecasts (
  workspace_id    TEXT   NOT NULL,
  id              TEXT   NOT NULL,
  plan_id         TEXT   NOT NULL,
  plan_version    BIGINT NOT NULL,
  milestone_id    TEXT   NOT NULL DEFAULT '',
  estimate_set_id TEXT   NOT NULL DEFAULT '',
  status          TEXT   NOT NULL,
  ledger_rev      BIGINT NOT NULL,
  day             BIGINT NOT NULL,
  created_at      BIGINT NOT NULL,
  body            TEXT   NOT NULL,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT forecast_status CHECK (status IN ('forecast', 'no_history', 'no_capacity', 'finished'))
);

CREATE UNIQUE INDEX forecasts_day ON forecasts (workspace_id, plan_id, plan_version, milestone_id, day);
