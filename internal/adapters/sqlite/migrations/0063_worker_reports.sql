-- What each worker last measured of its machine and its sessions, as JSON, and when it measured it by the
-- control plane's clock (FR-OBS-8). Every heartbeat that carries a report replaces it, with no event, so
-- the shared revision does not move with each measure.
ALTER TABLE workers ADD COLUMN report TEXT NOT NULL DEFAULT '';
ALTER TABLE workers ADD COLUMN reported_at BIGINT NOT NULL DEFAULT 0;
