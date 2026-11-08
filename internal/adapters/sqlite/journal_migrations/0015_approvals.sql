-- What each lane has asked and not heard back on, which outlasts this worker. A lane owns one row
-- per call, and an owner says what kind of principal a process's token stands for.

CREATE TABLE open_approvals (
  id          TEXT   PRIMARY KEY,
  lane        TEXT   NOT NULL,
  generation  BIGINT NOT NULL,
  tool        TEXT   NOT NULL,
  digest      TEXT   NOT NULL,
  deadline_ms BIGINT NOT NULL
);

CREATE UNIQUE INDEX open_approvals_call ON open_approvals (lane, digest);

ALTER TABLE lanes ADD COLUMN owner TEXT NOT NULL DEFAULT '';
