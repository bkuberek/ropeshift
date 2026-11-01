-- How many times a member reset a lane's breaker. A worker acts on a count it has not seen,
-- so a reset that crosses a restart is neither lost nor taken twice.

ALTER TABLE lanes ADD COLUMN resets BIGINT NOT NULL DEFAULT 0;
