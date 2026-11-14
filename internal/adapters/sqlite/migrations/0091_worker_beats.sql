-- When each worker's session last beat, by the control plane's clock. A worker that beats is connected, so
-- the assistant's placement reads it with the handshake. Every heartbeat replaces it, with no event, so the
-- shared revision does not move with each beat.
ALTER TABLE workers ADD COLUMN last_beat_at BIGINT NOT NULL DEFAULT 0;
