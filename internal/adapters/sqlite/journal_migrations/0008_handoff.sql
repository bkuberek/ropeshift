-- How far a lane's handoff got, so a restart goes on from there, and which charges pay for a
-- planned generation, which counts toward the day and not toward a crash loop.

ALTER TABLE lanes ADD COLUMN handoff BLOB;
ALTER TABLE lane_restarts ADD COLUMN planned BIGINT NOT NULL DEFAULT 0;
