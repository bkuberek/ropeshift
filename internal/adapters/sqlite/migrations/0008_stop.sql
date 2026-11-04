-- How a stop left a lane's worktree, a request to remove it, and what a lane left behind.

ALTER TABLE lanes ADD COLUMN unpushed_work BIGINT NOT NULL DEFAULT 0;
ALTER TABLE lanes ADD COLUMN archive_request TEXT NOT NULL DEFAULT '';
ALTER TABLE lanes ADD COLUMN archived BIGINT NOT NULL DEFAULT 0;

CREATE TABLE artifacts (
  workspace_id TEXT   NOT NULL,
  id           TEXT   NOT NULL,
  lane_id      TEXT   NOT NULL,
  kind         TEXT   NOT NULL,
  uri          TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  created_at   BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE INDEX artifacts_lane ON artifacts (workspace_id, lane_id);
