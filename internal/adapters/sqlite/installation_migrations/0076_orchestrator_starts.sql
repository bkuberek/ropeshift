-- A start of a worker's orchestrator a member scheduled for its sign-in's usage reset, which survives a restart, one
-- per worker. starts_at is the reset it waits for, in epoch milliseconds, and zero while it waits for the next reported
-- one; missed counts the resets that passed with the limit still holding. It keeps the columns a project's ledger
-- gives it, so a store with no installation reads and writes it with the same statements.
CREATE TABLE orchestrator_starts (
  workspace_id TEXT   NOT NULL,
  worker_id    TEXT   NOT NULL,
  starts_at    BIGINT NOT NULL DEFAULT 0,
  scheduled_by TEXT   NOT NULL,
  scheduled_at BIGINT NOT NULL,
  missed       BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, worker_id)
);
