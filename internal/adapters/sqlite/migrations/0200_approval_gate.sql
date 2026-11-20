-- A lane's request says whether its call is a gate: one the lane's profile gates, or a push, which asks in every mode
-- and which a member approves one call at a time, never in a batch. A tool call still pending when this column arrives
-- was never read against the lane's gates, so it reads as a gate and waits for its own review; any other request
-- raised before it reads as no gate.

ALTER TABLE approval_requests ADD COLUMN gate BIGINT NOT NULL DEFAULT 0;

UPDATE approval_requests SET gate = 1 WHERE state = 'pending' AND kind = 'tool_call';
