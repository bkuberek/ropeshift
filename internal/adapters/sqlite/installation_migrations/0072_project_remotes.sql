-- A new project's remotes (FR-TPL-6). A template write request holds the remote of the folder's own repository as
-- JSON text, empty for none: a URL to add as origin, or a repository to create on a forge. The remote of each new
-- code repository sits in repositories, and what the worker made of each repository sits in report, both JSON
-- already. A forge account read is a request that a worker report whether its forge CLI can create repositories, and
-- under which owners, as a template write is a request that it write files: offered for two minutes, settled by the
-- worker's first report. report is the worker's report as JSON. It keeps the columns a project's ledger gives it, so
-- a store with no installation reads and writes it with the same statements.
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
