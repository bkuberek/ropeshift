-- A question keeps when it was recorded, by database time, so the core takes to a person a question no live
-- orchestrator took within the batch window. A question recorded before this column reads as asked at zero, so its
-- window has passed.

ALTER TABLE questions ADD COLUMN asked_at BIGINT;

-- A start of a worker's orchestrator a member scheduled for its sign-in's usage reset, as the installation keeps it in
-- installation migration 0076, with the columns a store with no installation reads and writes with the same
-- statements. starts_at is the reset it waits for, and zero while it waits for the next reported one; missed counts
-- the resets that passed with the limit still holding.

CREATE TABLE orchestrator_starts (
  workspace_id TEXT   NOT NULL,
  worker_id    TEXT   NOT NULL,
  starts_at    BIGINT NOT NULL DEFAULT 0,
  scheduled_by TEXT   NOT NULL,
  scheduled_at BIGINT NOT NULL,
  missed       BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, worker_id)
);
