-- A new project's remotes (FR-TPL-6), as the installation database holds them (installation migration 0072). A store
-- with no installation keeps them here, under its own workspace, with the same columns and statements.
ALTER TABLE template_writes ADD COLUMN remote TEXT NOT NULL DEFAULT '';

CREATE TABLE forge_account_reads (
  workspace_id TEXT   NOT NULL,
  id           TEXT   NOT NULL,
  worker_id    TEXT   NOT NULL,
  requested_by TEXT   NOT NULL,
  requested_at BIGINT NOT NULL,
  expires_at   BIGINT NOT NULL,
  forge        TEXT   NOT NULL,
  state        TEXT   NOT NULL,
  report       TEXT   NOT NULL DEFAULT '',
  reported_at  BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, id)
);

CREATE INDEX forge_account_reads_worker ON forge_account_reads (workspace_id, worker_id, state, requested_at);
