-- How long a join token lives and whether a member ended it early, in milliseconds of the ledger's clock. A token
-- with no expiry, which the solo daemon's own worker uses at once, holds zero, and so does a token nobody revoked.
-- A join token that existed before this migration never expires.

ALTER TABLE join_grants ADD COLUMN expires_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE join_grants ADD COLUMN revoked_at BIGINT NOT NULL DEFAULT 0;
