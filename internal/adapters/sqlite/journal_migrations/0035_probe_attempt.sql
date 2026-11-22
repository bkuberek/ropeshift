-- The last qualification probe this worker began, kept before its first session starts, so a worker that restarts does
-- not begin another at once. It holds a version and a time, and nothing of a session or an account.

CREATE TABLE probe_attempt (
  id       INTEGER PRIMARY KEY CHECK (id = 1),
  version  TEXT NOT NULL,
  began_ms INTEGER NOT NULL
);
