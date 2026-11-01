-- Wake conditions, and the facts only a signal can establish. Times are BIGINT, because epoch
-- milliseconds overflow a 32-bit Postgres INTEGER. A lane has at most one open wake.

CREATE TABLE wake_conditions (
  workspace_id TEXT   NOT NULL,
  id           TEXT   NOT NULL,
  lane_id      TEXT   NOT NULL,
  expr         TEXT   NOT NULL,
  next_eval_at BIGINT,
  fired_at     BIGINT,
  fired_by     TEXT,
  stored_rev   BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE INDEX wake_due ON wake_conditions (workspace_id, next_eval_at)
  WHERE fired_at IS NULL;

CREATE UNIQUE INDEX wake_one_open ON wake_conditions (workspace_id, lane_id)
  WHERE fired_at IS NULL;

CREATE TABLE wake_signals (
  workspace_id TEXT   NOT NULL,
  kind         TEXT   NOT NULL,
  signal_key   TEXT   NOT NULL,
  rev          BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, kind, signal_key, rev)
);
