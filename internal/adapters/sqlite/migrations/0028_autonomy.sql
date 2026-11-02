-- What keeps a person near automated use (FR-ORCH-6). A level is optional, because a worker with none
-- starts where its sign-in does. The checks are the rule itself, so that no code path can get past
-- them: only a member acknowledges the stated risk, and only a member opens a run window.

CREATE TABLE worker_autonomy (
  workspace_id        TEXT   NOT NULL,
  worker_id           TEXT   NOT NULL,
  level               TEXT   NOT NULL,
  acknowledged_by     TEXT   NOT NULL,
  acknowledged_kind   TEXT   NOT NULL,
  acknowledged_digest TEXT   NOT NULL,
  acknowledged_at     BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, worker_id),
  CONSTRAINT worker_autonomy_level CHECK (level IN ('', 'manual', 'attended', 'unattended')),
  CONSTRAINT worker_autonomy_acknowledger CHECK (acknowledged_by = '' OR acknowledged_kind = 'member')
);

CREATE TABLE run_windows (
  workspace_id TEXT   NOT NULL,
  id           TEXT   NOT NULL,
  worker_id    TEXT   NOT NULL,
  opened_by    TEXT   NOT NULL,
  opened_kind  TEXT   NOT NULL,
  opened_at    BIGINT NOT NULL,
  closes_at    BIGINT NOT NULL,
  closed_by    TEXT   NOT NULL,
  closed_at    BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT run_window_opener CHECK (opened_kind = 'member'),
  CONSTRAINT run_window_has_an_end CHECK (closes_at > opened_at)
);

CREATE INDEX run_windows_worker ON run_windows (workspace_id, worker_id, closes_at);
