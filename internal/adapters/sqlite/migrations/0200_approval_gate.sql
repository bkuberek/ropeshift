-- A lane's request says whether its call is a gate: one the lane's profile gates, or a push, which asks in every mode
-- and which a member approves one call at a time, never in a batch. A request raised before this column reads as no
-- gate.

ALTER TABLE approval_requests ADD COLUMN gate BIGINT NOT NULL DEFAULT 0;
