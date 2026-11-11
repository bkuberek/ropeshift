-- How many times an orchestrator reset a lane's breaker within the recovery policy (ADR-0014). Such a reset
-- clears the breaker's window and leaves the lane's day, so a worker counts it apart from a member's reset.

ALTER TABLE lanes ADD COLUMN window_resets BIGINT NOT NULL DEFAULT 0;
