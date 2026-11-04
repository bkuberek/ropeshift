-- The state a compaction under way left, which its end returns to.

ALTER TABLE lanes ADD COLUMN compact_from TEXT NOT NULL DEFAULT '';
