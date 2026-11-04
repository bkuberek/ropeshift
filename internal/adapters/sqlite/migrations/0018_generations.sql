-- A generation's own record, because a vendor's session ID does not identify one, and the
-- handoff documents a later generation reads. One session can span generations, so nothing
-- is unique on it.

CREATE TABLE generations (
  workspace_id TEXT   NOT NULL,
  lane_id      TEXT   NOT NULL,
  generation   BIGINT NOT NULL,
  start_reason TEXT   NOT NULL,
  end_reason   TEXT   NOT NULL DEFAULT '',
  session_id   TEXT   NOT NULL DEFAULT '',
  handoff_in   TEXT   NOT NULL DEFAULT '',
  handoff_out  TEXT   NOT NULL DEFAULT '',
  started_at   BIGINT NOT NULL,
  ended_at     BIGINT,
  PRIMARY KEY (workspace_id, lane_id, generation)
);

CREATE TABLE handoffs (
  workspace_id TEXT   NOT NULL,
  lane_id      TEXT   NOT NULL,
  generation   BIGINT NOT NULL,
  artifact_id  TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  degraded     BIGINT NOT NULL,
  body         TEXT   NOT NULL,
  created_at   BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, lane_id, generation)
);
