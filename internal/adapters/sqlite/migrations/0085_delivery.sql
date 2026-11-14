-- The delivery fold's read models (ADR-0026). Every row is derived from the log, and a new derivation
-- version drops them all and folds the log again. delivery_fold holds the fold's own state as JSON: the
-- version, the revision it read through, when it first ran, and the work in progress it counts.
CREATE TABLE delivery_fold (
  workspace_id TEXT NOT NULL PRIMARY KEY,
  state        TEXT NOT NULL
);

-- Each span in which the workspace's work-in-progress cap was full.
CREATE TABLE delivery_wip_full (
  workspace_id TEXT   NOT NULL,
  from_at      BIGINT NOT NULL,
  to_at        BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, from_at)
);

-- Each finished work order's timeline, as JSON.
CREATE TABLE work_timelines (
  workspace_id  TEXT   NOT NULL,
  work_order_id TEXT   NOT NULL,
  version       BIGINT NOT NULL,
  ended_at      BIGINT NOT NULL,
  body          TEXT   NOT NULL,
  PRIMARY KEY (workspace_id, work_order_id)
);

-- Each finished change's or rework's delivery record, as JSON, with the keys look back groups by.
CREATE TABLE delivery_records (
  workspace_id     TEXT   NOT NULL,
  work_order_id    TEXT   NOT NULL,
  version          BIGINT NOT NULL,
  kind             TEXT   NOT NULL,
  size             TEXT   NOT NULL,
  capability_class TEXT   NOT NULL,
  repository_id    TEXT   NOT NULL,
  outcome          TEXT   NOT NULL,
  ended_at         BIGINT NOT NULL,
  body             TEXT   NOT NULL,
  PRIMARY KEY (workspace_id, work_order_id)
);

CREATE INDEX delivery_records_ended ON delivery_records (workspace_id, ended_at);
