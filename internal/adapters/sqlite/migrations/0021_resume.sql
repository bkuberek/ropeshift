-- What a resume reads: when a generation's model last answered, in database time, and when a
-- preferred worker's first claim on a woken lane runs out.

ALTER TABLE generations ADD COLUMN last_response_at BIGINT;
ALTER TABLE lanes ADD COLUMN resume_after BIGINT;
