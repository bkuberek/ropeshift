-- The roster agent a claim's lane runs as, and the roster it started with (ADR-0025): the version of each
-- layer it pinned, zero for a layer with none, and the digest of the roster they resolve to. A claim that
-- brings a parked lane back keeps its first claim's, so a lane runs as one agent with one roster.
ALTER TABLE claims ADD COLUMN agent TEXT NOT NULL DEFAULT '';
ALTER TABLE claims ADD COLUMN roster_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE claims ADD COLUMN roster_installation BIGINT NOT NULL DEFAULT 0;
ALTER TABLE claims ADD COLUMN roster_project BIGINT NOT NULL DEFAULT 0;
