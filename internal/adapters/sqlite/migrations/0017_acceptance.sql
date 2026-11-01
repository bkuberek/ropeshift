-- The check results that stay linked to a work order, and the attempt each artifact belongs
-- to. A fence names an attempt, because a lane's end moves it.

ALTER TABLE artifacts ADD COLUMN fence BIGINT NOT NULL DEFAULT 0;

CREATE INDEX artifacts_lane_fence ON artifacts (workspace_id, lane_id, fence);

CREATE TABLE check_results (
  workspace_id  TEXT   NOT NULL,
  id            TEXT   NOT NULL,
  work_order_id TEXT   NOT NULL,
  lane_id       TEXT   NOT NULL,
  fence         BIGINT NOT NULL,
  artifact_id   TEXT   NOT NULL,
  criterion_id  TEXT   NOT NULL,
  name          TEXT   NOT NULL,
  status        TEXT   NOT NULL,
  source        TEXT   NOT NULL,
  details_uri   TEXT   NOT NULL,
  created_at    BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE INDEX check_results_attempt ON check_results (workspace_id, work_order_id, fence);
