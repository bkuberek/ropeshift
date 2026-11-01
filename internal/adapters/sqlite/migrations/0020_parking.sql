-- What a parked generation left, and where its lane can resume. A lane parks at most once in
-- a generation, so one row per generation holds every checkpoint.

CREATE TABLE checkpoints (
  workspace_id        TEXT   NOT NULL,
  lane_id             TEXT   NOT NULL,
  generation          BIGINT NOT NULL,
  branch              TEXT   NOT NULL DEFAULT '',
  head_sha            TEXT   NOT NULL DEFAULT '',
  snapshot_ref        TEXT   NOT NULL DEFAULT '',
  pushed              BIGINT NOT NULL DEFAULT 0,
  handoff_artifact_id TEXT   NOT NULL DEFAULT '',
  pending_instruction TEXT   NOT NULL DEFAULT '',
  created_at          BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, lane_id, generation)
);

ALTER TABLE lanes ADD COLUMN resume_affinity TEXT NOT NULL DEFAULT '';
ALTER TABLE lanes ADD COLUMN resume_worker TEXT NOT NULL DEFAULT '';
