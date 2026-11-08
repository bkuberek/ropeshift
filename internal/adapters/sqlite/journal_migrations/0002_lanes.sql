-- What the worker keeps about its lanes, and the hook events merged by meaning.

CREATE TABLE lanes (
  id           TEXT PRIMARY KEY,
  work_order   TEXT   NOT NULL,
  claim        TEXT   NOT NULL,
  fence        BIGINT NOT NULL,
  generation   BIGINT NOT NULL,
  state        TEXT   NOT NULL,
  token_digest TEXT,
  worktree     TEXT   NOT NULL DEFAULT '',
  branch       TEXT   NOT NULL DEFAULT '',
  report       BLOB
);

CREATE UNIQUE INDEX lanes_token ON lanes (token_digest);

CREATE TABLE hook_events (
  lane       TEXT   NOT NULL,
  generation BIGINT NOT NULL,
  event      TEXT   NOT NULL,
  subject    TEXT   NOT NULL,
  session_id TEXT   NOT NULL DEFAULT '',
  tool       TEXT   NOT NULL DEFAULT '',
  detail     TEXT   NOT NULL DEFAULT '',
  malformed  BIGINT NOT NULL DEFAULT 0,
  first_ms   BIGINT NOT NULL,
  last_ms    BIGINT NOT NULL,
  count      BIGINT NOT NULL,
  PRIMARY KEY (lane, generation, event, subject)
);
