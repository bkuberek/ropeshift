-- Each baseline (ADR-0026, FR-EST-10): the estimate set and the forecasts a plan approval showed, recorded in the
-- approval's transaction, or the estimates a work order made outside a plan was made with, recorded when it was
-- made, whose approval_id is empty. estimate_set_id and set_digest name the set it showed, and are empty when it
-- showed none. body is the baseline as JSON with the forecasts it showed, kept whole, because the forecast tick
-- replaces a day's forecast later that day. A baseline never changes, and a work order's is the first recorded
-- whose set holds an estimate of it.
CREATE TABLE estimate_baselines (
  workspace_id    TEXT   NOT NULL,
  id              TEXT   NOT NULL,
  approval_id     TEXT   NOT NULL DEFAULT '',
  plan_id         TEXT   NOT NULL DEFAULT '',
  plan_version    BIGINT NOT NULL DEFAULT 0,
  estimate_set_id TEXT   NOT NULL DEFAULT '',
  set_digest      TEXT   NOT NULL DEFAULT '',
  ledger_rev      BIGINT NOT NULL,
  created_at      BIGINT NOT NULL,
  body            TEXT   NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE UNIQUE INDEX estimate_baselines_approval ON estimate_baselines (workspace_id, approval_id) WHERE approval_id <> '';
CREATE INDEX estimate_baselines_set ON estimate_baselines (workspace_id, estimate_set_id);
CREATE INDEX estimate_baselines_plan ON estimate_baselines (workspace_id, plan_id, ledger_rev);

-- A work order's baseline is read from every estimate made of it.
CREATE INDEX estimates_work_order ON estimates (workspace_id, work_order_id);
