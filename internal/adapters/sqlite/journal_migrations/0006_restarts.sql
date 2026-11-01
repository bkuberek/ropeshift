-- What a worker needs to restart a session after a crash, and to do it once. Each charge
-- has one row for good, so a charge a crash interrupted cannot count twice.

ALTER TABLE lanes ADD COLUMN resume_session TEXT NOT NULL DEFAULT '';
ALTER TABLE lanes ADD COLUMN resets_seen BIGINT NOT NULL DEFAULT 0;

CREATE TABLE lane_restarts (
  lane          TEXT   NOT NULL,
  key           TEXT   NOT NULL,
  at_ms         BIGINT NOT NULL,
  not_before_ms BIGINT NOT NULL,
  PRIMARY KEY (lane, key)
);
