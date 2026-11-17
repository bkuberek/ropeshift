-- The requests a member made of a worker to write a shape's starting files into a folder on its machine
-- (FR-TPL-5), as the installation database holds them (installation migration 0062). A store with no
-- installation keeps them here, under its own workspace, with the same columns and statements.
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
