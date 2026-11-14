-- Each estimate set (ADR-0026, FR-EST-10): the estimates made together at one ledger revision, for the open
-- work orders of a plan version intake recorded, or for one work order made outside a plan. plan_id is empty
-- for the latter. digest covers what the set's estimates say. A set never changes.
CREATE TABLE estimate_sets (
  workspace_id      TEXT   NOT NULL,
  id                TEXT   NOT NULL,
  plan_id           TEXT   NOT NULL DEFAULT '',
  plan_version      BIGINT NOT NULL DEFAULT 0,
  ledger_rev        BIGINT NOT NULL,
  estimator_version BIGINT NOT NULL,
  policy_version    BIGINT NOT NULL,
  digest            TEXT   NOT NULL,
  created_at        BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE INDEX estimate_sets_plan ON estimate_sets (workspace_id, plan_id, plan_version, created_at);

-- Each estimate of a set, as JSON with the explanation it showed (FR-EST-14), and whether it is its work
-- order's current estimate: the one its newest set holds. A later set of the work order clears the flag.
CREATE TABLE estimates (
  workspace_id    TEXT   NOT NULL,
  id              TEXT   NOT NULL,
  estimate_set_id TEXT   NOT NULL,
  work_order_id   TEXT   NOT NULL,
  status          TEXT   NOT NULL,
  confidence      TEXT   NOT NULL DEFAULT '',
  current         BIGINT NOT NULL DEFAULT 0,
  created_at      BIGINT NOT NULL,
  body            TEXT   NOT NULL,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT estimate_status CHECK (status IN ('estimated', 'no_history'))
);

CREATE INDEX estimates_set ON estimates (workspace_id, estimate_set_id, work_order_id);
CREATE UNIQUE INDEX estimates_current ON estimates (workspace_id, work_order_id) WHERE current <> 0;
