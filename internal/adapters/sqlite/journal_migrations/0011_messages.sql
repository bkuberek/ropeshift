-- What this worker holds of messages to its lanes. The ID is what deduplicates a message the
-- control plane offers again, so a row stays for as long as the journal does.

CREATE TABLE messages (
  id              TEXT PRIMARY KEY,
  input_id        TEXT NOT NULL UNIQUE,
  lane            TEXT NOT NULL,
  generation      INTEGER NOT NULL,
  from_kind       TEXT NOT NULL,
  reply_to        TEXT NOT NULL,
  kind            TEXT NOT NULL,
  body            TEXT NOT NULL,
  traceparent     TEXT NOT NULL,
  age_ms          INTEGER NOT NULL,
  taken_ms        INTEGER NOT NULL,
  stale_after_ms  INTEGER NOT NULL,
  expire_after_ms INTEGER NOT NULL,
  state           TEXT NOT NULL
);

CREATE INDEX messages_state ON messages (state);
