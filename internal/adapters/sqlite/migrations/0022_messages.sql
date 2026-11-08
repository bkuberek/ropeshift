-- Messages to lanes. A row is the envelope and how far it got, and the event log holds every
-- change of it. A sender's sequence is unique, because a retry reads the first attempt.

CREATE TABLE messages (
  workspace_id  TEXT   NOT NULL,
  id            TEXT   NOT NULL,
  envelope      BIGINT NOT NULL,
  to_lane       TEXT   NOT NULL,
  to_generation BIGINT NOT NULL,
  from_kind     TEXT   NOT NULL,
  from_id       TEXT   NOT NULL,
  sender_seq    BIGINT NOT NULL,
  kind          TEXT   NOT NULL,
  reply_to      TEXT,
  body          TEXT   NOT NULL,
  state         TEXT   NOT NULL,
  reason        TEXT,
  reply         TEXT,
  carried_in    BIGINT,
  created_at    BIGINT NOT NULL,
  stale_at      BIGINT NOT NULL,
  expires_at    BIGINT NOT NULL,
  traceparent   TEXT,
  PRIMARY KEY (workspace_id, id)
);

CREATE UNIQUE INDEX messages_sender_seq ON messages (workspace_id, from_kind, from_id, sender_seq);
CREATE INDEX messages_lane ON messages (workspace_id, to_lane, state);
CREATE INDEX messages_due ON messages (workspace_id, expires_at);
