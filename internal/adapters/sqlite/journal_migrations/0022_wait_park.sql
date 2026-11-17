-- The end, in milliseconds, of a `lane_wait` longer than the live bound that no park has begun for yet, or 0 when
-- none waits (FR-SPEND-1). It is written before the lane is told to park, and the park's own write clears it, so a
-- restart between the two still parks the lane with its timer instead of leaving it live and idle.

ALTER TABLE lanes ADD COLUMN wait_park_at BIGINT NOT NULL DEFAULT 0;
