-- Context packs. A version never changes once it is written, and `body` is the one form a session
-- reads, so it stays byte-identical across generations. The items are the index a notice finds its
-- lanes through, and `record_id` is what stays the same across a record's versions.

CREATE TABLE context_packs (
  workspace_id    TEXT   NOT NULL,
  id              TEXT   NOT NULL,
  version         BIGINT NOT NULL,
  work_order_id   TEXT   NOT NULL,
  digest          TEXT   NOT NULL,
  size_estimate   BIGINT NOT NULL,
  body            TEXT   NOT NULL,
  created_at      BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id, version)
);

CREATE TABLE context_pack_items (
  workspace_id TEXT   NOT NULL,
  pack_id      TEXT   NOT NULL,
  version      BIGINT NOT NULL,
  position     BIGINT NOT NULL,
  kind         TEXT   NOT NULL,
  ref          TEXT   NOT NULL,
  record_id    TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  trust        TEXT   NOT NULL,
  indexed      BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, pack_id, version, position)
);

CREATE INDEX context_pack_items_record ON context_pack_items (workspace_id, kind, record_id);

-- What a member or an orchestrator added to a work order's next pack. Knowledge brings its text,
-- because a claim's transaction reaches no outside system.
CREATE TABLE context_pack_additions (
  workspace_id  TEXT   NOT NULL,
  work_order_id TEXT   NOT NULL,
  kind          TEXT   NOT NULL,
  ref           TEXT   NOT NULL,
  body          TEXT   NOT NULL,
  added_by      TEXT   NOT NULL,
  added_at      BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, work_order_id, kind, ref)
);

ALTER TABLE claims ADD COLUMN pack_id TEXT;

-- Each generation records the version it read, and what it held at its first call.
ALTER TABLE generations ADD COLUMN pack_id TEXT NOT NULL DEFAULT '';
ALTER TABLE generations ADD COLUMN pack_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE generations ADD COLUMN baseline_size BIGINT NOT NULL DEFAULT 0;
