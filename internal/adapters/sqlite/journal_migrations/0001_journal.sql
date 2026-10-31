-- The worker's journal: who it is, what it has not uploaded, what it holds, and the
-- last desired state it read.

CREATE TABLE identity (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  worker_id  TEXT    NOT NULL,
  credential TEXT    NOT NULL,
  epoch      BIGINT  NOT NULL
);

CREATE TABLE events (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  epoch       BIGINT NOT NULL,
  type        TEXT   NOT NULL,
  work_order  TEXT,
  claim_id    TEXT,
  fence       BIGINT,
  lane        TEXT,
  generation  BIGINT,
  payload     BLOB,
  occurred_at BIGINT NOT NULL
);

CREATE TABLE rejections (
  seq           INTEGER PRIMARY KEY,
  status        TEXT   NOT NULL,
  current_fence BIGINT,
  state         TEXT,
  detail        TEXT
);

CREATE TABLE cursors (
  name  TEXT PRIMARY KEY,
  value BIGINT NOT NULL
);

CREATE TABLE leases (
  work_order TEXT PRIMARY KEY,
  claim      TEXT   NOT NULL,
  fence      BIGINT NOT NULL
);

CREATE TABLE desired (
  id   INTEGER PRIMARY KEY CHECK (id = 1),
  body TEXT NOT NULL
);
