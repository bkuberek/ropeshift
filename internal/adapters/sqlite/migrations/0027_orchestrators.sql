-- One agent session per worker, supervised like a lane, with no work order and no outcome. Only a
-- member starts one, so no agent runs before a person has said so.
CREATE TABLE orchestrators (
  workspace_id  TEXT NOT NULL,
  id            TEXT NOT NULL,
  worker_id     TEXT NOT NULL,
  desired_state TEXT NOT NULL,
  run           BIGINT NOT NULL DEFAULT 0,
  resets        BIGINT NOT NULL DEFAULT 0,
  actual_state  TEXT NOT NULL DEFAULT '',
  generation    BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, worker_id),
  CONSTRAINT orchestrator_desired CHECK (desired_state IN ('running', 'stopped'))
);
