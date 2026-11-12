-- The installation's layer of the agent roster (ADR-0025), which every project resolves under its own. It
-- keeps a ledger's columns, so the Store's statements serve both.
CREATE TABLE roster_versions (
  workspace_id TEXT   NOT NULL,
  scope        TEXT   NOT NULL,
  version      BIGINT NOT NULL,
  id           TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  body         TEXT   NOT NULL,
  summary      TEXT   NOT NULL,
  authored_by  TEXT   NOT NULL,
  prepared_by  TEXT   NOT NULL DEFAULT '',
  created_at   BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, scope, version),
  CONSTRAINT roster_is_installation CHECK (scope = 'installation'),
  CONSTRAINT roster_version CHECK (version >= 1)
);
