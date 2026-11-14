-- The assistant policy (ADR-0024) lives beside the autonomy policy, because both govern sessions that run on
-- workers every project shares. The table held the autonomy policy alone, and its check says so.
CREATE TABLE policy_versions_next (
  workspace_id TEXT   NOT NULL,
  kind         TEXT   NOT NULL,
  version      BIGINT NOT NULL,
  id           TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  body         TEXT   NOT NULL,
  settings     TEXT   NOT NULL,
  authored_by  TEXT   NOT NULL,
  created_at   BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, kind, version),
  CONSTRAINT policy_is_installations CHECK (kind IN ('autonomy', 'assistant'))
);

INSERT INTO policy_versions_next (workspace_id, kind, version, id, digest, body, settings, authored_by, created_at)
SELECT workspace_id, kind, version, id, digest, body, settings, authored_by, created_at FROM policy_versions;

DROP TABLE policy_versions;

ALTER TABLE policy_versions_next RENAME TO policy_versions;
