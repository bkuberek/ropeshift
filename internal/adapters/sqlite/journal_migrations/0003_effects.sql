-- The effect ledger brackets every external effect, and a lane keeps what a restart
-- needs to read its session from where this worker stopped.

CREATE TABLE effects (
  key        TEXT PRIMARY KEY,
  kind       TEXT   NOT NULL,
  lane       TEXT   NOT NULL,
  generation BIGINT NOT NULL,
  target     TEXT   NOT NULL DEFAULT '',
  state      TEXT   NOT NULL,
  attempts   BIGINT NOT NULL DEFAULT 1,
  detail     TEXT   NOT NULL DEFAULT ''
);

CREATE INDEX effects_lane ON effects (lane);

ALTER TABLE lanes ADD COLUMN session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE lanes ADD COLUMN progress BLOB;
