-- How many times a member or an orchestrator asked for a lane's next generation. A worker compares
-- this with what it has acted on, as it does with a lane's resets.

ALTER TABLE lanes ADD COLUMN respawns BIGINT NOT NULL DEFAULT 0;
