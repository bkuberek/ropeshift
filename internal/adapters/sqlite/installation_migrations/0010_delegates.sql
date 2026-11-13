-- Delegate credentials (ADR-0024): a credential a member makes for an agent outside ropeshift, bound to one
-- project. It is kept as its token's digest, as a session is, and never as the token. A credential that no
-- longer acts stays until its member removes it, so the audit can still name it.
CREATE TABLE delegate_credentials (
  workspace_id TEXT   NOT NULL,
  id           TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  member_id    TEXT   NOT NULL,
  project_id   TEXT   NOT NULL,
  label        TEXT   NOT NULL,
  kind         TEXT   NOT NULL,
  created_at   BIGINT NOT NULL,
  expires_at   BIGINT NOT NULL,
  last_used_at BIGINT,
  revoked_at   BIGINT,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT delegate_life CHECK (expires_at > created_at),
  CONSTRAINT delegate_label CHECK (label <> '')
);

CREATE UNIQUE INDEX delegate_credentials_digest ON delegate_credentials (workspace_id, digest);
CREATE INDEX delegate_credentials_member ON delegate_credentials (workspace_id, member_id);
