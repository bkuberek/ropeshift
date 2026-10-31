-- The ledger core. Every table has workspace_id in its primary key, and times are
-- UTC epoch milliseconds in BIGINT.

CREATE TABLE workspaces (
  id             TEXT   NOT NULL PRIMARY KEY,
  name           TEXT   NOT NULL,
  fleet_mode     TEXT   NOT NULL,
  rev            BIGINT NOT NULL,
  policy_version BIGINT NOT NULL
);

CREATE TABLE events (
  workspace_id TEXT   NOT NULL,
  rev          BIGINT NOT NULL,
  id           TEXT   NOT NULL,
  type         TEXT   NOT NULL,
  actor_kind   TEXT   NOT NULL,
  actor_id     TEXT   NOT NULL,
  subject_kind TEXT   NOT NULL,
  subject_id   TEXT   NOT NULL,
  data         TEXT   NOT NULL,
  occurred_at  BIGINT,
  recorded_at  BIGINT NOT NULL,
  traceparent  TEXT,
  PRIMARY KEY (workspace_id, rev)
);

CREATE UNIQUE INDEX events_id ON events (workspace_id, id);

CREATE TABLE work_orders (
  workspace_id     TEXT    NOT NULL,
  id               TEXT    NOT NULL,
  kind             TEXT    NOT NULL,
  state            TEXT    NOT NULL,
  title            TEXT,
  repository_id    TEXT,
  priority         BIGINT  NOT NULL DEFAULT 0,
  size             TEXT,
  requirements     TEXT,
  needs            TEXT,
  contract_id      TEXT,
  contract_version BIGINT,
  fence            BIGINT  NOT NULL DEFAULT 0,
  claim_id         TEXT,
  lease_worker     TEXT,
  lease_until      BIGINT,
  takeover_after   BIGINT,
  branch           TEXT,
  resume_lane_id   TEXT,
  rework_of        TEXT,
  attempts         BIGINT  NOT NULL DEFAULT 0,
  max_attempts     BIGINT  NOT NULL DEFAULT 0,
  deadline         BIGINT,
  rev              BIGINT  NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE INDEX work_orders_state ON work_orders (workspace_id, state);
