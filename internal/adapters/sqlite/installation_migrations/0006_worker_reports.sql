-- What each worker last measured of its machine and its sessions, as a ledger's workers table holds it.
ALTER TABLE workers ADD COLUMN report TEXT NOT NULL DEFAULT '';
ALTER TABLE workers ADD COLUMN reported_at BIGINT NOT NULL DEFAULT 0;
