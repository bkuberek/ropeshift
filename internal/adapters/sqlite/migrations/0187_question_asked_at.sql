-- A question keeps when it was recorded, by database time, so the core takes to a person a question no live
-- orchestrator took within the batch window. A question recorded before this column reads as asked at zero, so its
-- window has passed.

ALTER TABLE questions ADD COLUMN asked_at BIGINT;
