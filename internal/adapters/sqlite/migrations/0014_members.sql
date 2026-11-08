-- Who can sign in, and who has. Each holds a digest and no secret value.

CREATE TABLE members (
  workspace_id     TEXT NOT NULL,
  id               TEXT NOT NULL,
  identity_subject TEXT NOT NULL,
  display_name     TEXT NOT NULL,
  status           TEXT NOT NULL,
  proof_digest     TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE member_sessions (
  workspace_id TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  member_id    TEXT   NOT NULL,
  created_at   BIGINT NOT NULL,
  expires_at   BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, digest)
);

CREATE INDEX member_sessions_expiry ON member_sessions (workspace_id, expires_at);
