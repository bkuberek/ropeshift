-- What a worker last said about each lane. A status arrives as an uploaded event, so it
-- takes the workspace revision, and a fenced worker cannot move it.

ALTER TABLE lanes ADD COLUMN worker_id TEXT NOT NULL DEFAULT '';
ALTER TABLE lanes ADD COLUMN actual_state TEXT NOT NULL DEFAULT 'pending';
ALTER TABLE lanes ADD COLUMN conditions TEXT NOT NULL DEFAULT '';
ALTER TABLE lanes ADD COLUMN liveness TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE lanes ADD COLUMN reach_model TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE lanes ADD COLUMN reach_ledger TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE lanes ADD COLUMN reported_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE lanes ADD COLUMN reported_by_epoch BIGINT NOT NULL DEFAULT 0;
