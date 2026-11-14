-- When each worker's session last beat, as a ledger's workers table holds it.
ALTER TABLE workers ADD COLUMN last_beat_at BIGINT NOT NULL DEFAULT 0;
