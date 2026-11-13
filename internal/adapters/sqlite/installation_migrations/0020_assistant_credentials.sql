-- Assistant credentials (ADR-0024): the control plane makes one for each generation of the session of a
-- member's conversation with the assistant, naming the conversation and the generation. It has no expiry,
-- which a zero expires_at records, and acts only while that generation runs. A member makes only external
-- credentials, which expire.
CREATE TABLE delegate_credentials_next (
  workspace_id    TEXT   NOT NULL,
  id              TEXT   NOT NULL,
  digest          TEXT   NOT NULL,
  member_id       TEXT   NOT NULL,
  project_id      TEXT   NOT NULL,
  label           TEXT   NOT NULL,
  kind            TEXT   NOT NULL,
  created_at      BIGINT NOT NULL,
  expires_at      BIGINT NOT NULL,
  last_used_at    BIGINT,
  revoked_at      BIGINT,
  conversation_id TEXT,
  generation      BIGINT,
  ended_reason    TEXT,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT delegate_kind CHECK (kind IN ('external', 'assistant')),
  CONSTRAINT delegate_life CHECK ((kind = 'external' AND expires_at > created_at) OR (kind = 'assistant' AND expires_at = 0)),
  CONSTRAINT delegate_label CHECK (label <> ''),
  CONSTRAINT delegate_assistant_names_its_session CHECK (
    (kind = 'assistant' AND conversation_id IS NOT NULL AND generation IS NOT NULL)
    OR (kind = 'external' AND conversation_id IS NULL AND generation IS NULL)),
  -- Why a revoked credential ended: an assistant's generation or session ended, which leaves its cards to a
  -- member, or a member revoked it or signed out of every session, which ends them.
  CONSTRAINT delegate_end CHECK (
    (revoked_at IS NULL AND ended_reason IS NULL)
    OR (revoked_at IS NOT NULL AND ended_reason IN ('generation_ended', 'session_stopped', 'idle_stopped', 'revoked', 'signed_out')))
);

INSERT INTO delegate_credentials_next
  (workspace_id, id, digest, member_id, project_id, label, kind, created_at, expires_at, last_used_at, revoked_at, ended_reason)
SELECT workspace_id, id, digest, member_id, project_id, label, kind, created_at, expires_at, last_used_at, revoked_at,
       CASE WHEN revoked_at IS NOT NULL THEN 'revoked' END
FROM delegate_credentials;

DROP TABLE delegate_credentials;

ALTER TABLE delegate_credentials_next RENAME TO delegate_credentials;

CREATE UNIQUE INDEX delegate_credentials_digest ON delegate_credentials (workspace_id, digest);
CREATE INDEX delegate_credentials_member ON delegate_credentials (workspace_id, member_id);
CREATE INDEX delegate_credentials_conversation ON delegate_credentials (workspace_id, conversation_id);
