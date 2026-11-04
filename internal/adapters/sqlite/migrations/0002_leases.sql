-- Claims, the part of a lane that fencing reads, and the work-in-progress limit.

ALTER TABLE workspaces ADD COLUMN wip_limit BIGINT NOT NULL DEFAULT 0;

CREATE TABLE claims (
  workspace_id  TEXT   NOT NULL,
  id            TEXT   NOT NULL,
  work_order_id TEXT   NOT NULL,
  worker_id     TEXT   NOT NULL,
  fence         BIGINT NOT NULL,
  granted_at    BIGINT NOT NULL,
  ended_at      BIGINT,
  end_reason    TEXT,
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE lanes (
  workspace_id       TEXT   NOT NULL,
  id                 TEXT   NOT NULL,
  work_order_id      TEXT   NOT NULL,
  current_generation BIGINT NOT NULL,
  started_fence      BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);
