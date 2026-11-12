-- How many of an orchestrator's resets of a lane's breaker window this worker has acted on.

ALTER TABLE lanes ADD COLUMN window_resets_seen BIGINT NOT NULL DEFAULT 0;
