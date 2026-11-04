-- What intake records: plans, contract versions, external keys, dependencies, and
-- how far each source has been read.

ALTER TABLE work_orders ADD COLUMN reviewed_by TEXT;

CREATE TABLE plans (
  workspace_id TEXT NOT NULL,
  id           TEXT NOT NULL,
  goal_id      TEXT,
  title        TEXT NOT NULL,
  status       TEXT NOT NULL,
  source       TEXT,
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE contracts (
  workspace_id TEXT   NOT NULL,
  id           TEXT   NOT NULL,
  version      BIGINT NOT NULL,
  plan_id      TEXT   NOT NULL,
  title        TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  source       TEXT,
  statement    TEXT   NOT NULL,
  criteria     TEXT   NOT NULL,
  requirements TEXT   NOT NULL,
  decisions    TEXT   NOT NULL,
  status       TEXT   NOT NULL,
  PRIMARY KEY (workspace_id, id, version)
);

CREATE TABLE external_links (
  workspace_id TEXT NOT NULL,
  record_kind  TEXT NOT NULL,
  adapter      TEXT NOT NULL,
  instance     TEXT NOT NULL,
  native_id    TEXT NOT NULL,
  record_id    TEXT NOT NULL,
  display_key  TEXT,
  PRIMARY KEY (workspace_id, record_kind, adapter, instance, native_id)
);

CREATE TABLE dependencies (
  workspace_id  TEXT NOT NULL,
  work_order_id TEXT NOT NULL,
  depends_on    TEXT NOT NULL,
  adapter       TEXT,
  instance      TEXT,
  kind          TEXT NOT NULL,
  PRIMARY KEY (workspace_id, work_order_id, depends_on)
);

CREATE TABLE source_bindings (
  workspace_id TEXT NOT NULL,
  id           TEXT NOT NULL,
  port         TEXT NOT NULL,
  adapter      TEXT NOT NULL,
  instance_url TEXT NOT NULL,
  cursor       TEXT,
  health       TEXT NOT NULL,
  detail       TEXT,
  problems     TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);
