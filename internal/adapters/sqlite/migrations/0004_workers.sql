-- The worker plane: join grants and registered workers. Each holds a digest and no
-- secret value.

CREATE TABLE join_grants (
  workspace_id TEXT NOT NULL,
  id           TEXT NOT NULL,
  digest       TEXT NOT NULL,
  created_by   TEXT NOT NULL,
  used_by      TEXT,
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE workers (
  workspace_id      TEXT   NOT NULL,
  id                TEXT   NOT NULL,
  name              TEXT   NOT NULL,
  epoch             BIGINT NOT NULL,
  protocol          BIGINT NOT NULL,
  capabilities      TEXT   NOT NULL,
  proof_digest      TEXT   NOT NULL,
  last_handshake_at BIGINT,
  PRIMARY KEY (workspace_id, id)
);
