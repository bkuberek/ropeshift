-- The requests a member made of a worker to write a shape's starting files into a folder on its machine
-- (FR-TPL-5): the desired item's fields, who asked and when, where the request stands, and what the worker
-- reported. A request is offered to its worker for two minutes. State is offered until the worker's first
-- report, then written or refused; a request nobody reported by expires_at reads as expired, which no row
-- stores. repositories is a JSON array of the code repositories of a workspace, and report the worker's report
-- as JSON. It keeps the columns a project's ledger gives it, so a store with no installation reads and writes
-- it with the same statements.
CREATE TABLE template_writes (
  workspace_id TEXT   NOT NULL,
  id           TEXT   NOT NULL,
  worker_id    TEXT   NOT NULL,
  requested_by TEXT   NOT NULL,
  requested_at BIGINT NOT NULL,
  expires_at   BIGINT NOT NULL,
  shape        TEXT   NOT NULL,
  path         TEXT   NOT NULL,
  project      TEXT   NOT NULL DEFAULT '',
  repositories TEXT   NOT NULL DEFAULT '[]',
  first_commit BIGINT NOT NULL DEFAULT 0,
  state        TEXT   NOT NULL,
  report       TEXT   NOT NULL DEFAULT '',
  reported_at  BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, id)
);

CREATE INDEX template_writes_worker ON template_writes (workspace_id, worker_id, state, requested_at);
