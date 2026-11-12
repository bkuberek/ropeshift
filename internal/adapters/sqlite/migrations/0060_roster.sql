-- The agent roster (ADR-0025). Each version holds the whole layer of its scope as its body, so a revert
-- records an earlier body as a new version, and the history reads each version's difference from the one
-- before it. A project's ledger holds its own scope. A store with no installation database keeps the
-- installation's scope here too, under the same workspace.
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
  CONSTRAINT roster_scope CHECK (scope IN ('installation', 'project')),
  CONSTRAINT roster_version CHECK (version >= 1)
);

-- The roster agent a work order names for its lane, which a plan proposes and its approval covers. Empty
-- leaves the choice to the roster's default mapping.
ALTER TABLE work_orders ADD COLUMN agent TEXT NOT NULL DEFAULT '';
