-- The generation whose process runs now, which a clear does not change, because everything a
-- session left on this machine is named by the generation it started in. Zero means no process
-- has started since the generation began. respawns_seen counts the member requests this worker
-- has acted on, and a charge says when a person's reset, and not a crash, led to it.

ALTER TABLE lanes ADD COLUMN process_generation BIGINT NOT NULL DEFAULT 0;
ALTER TABLE lanes ADD COLUMN respawns_seen BIGINT NOT NULL DEFAULT 0;
ALTER TABLE lane_restarts ADD COLUMN reset BIGINT NOT NULL DEFAULT 0;
