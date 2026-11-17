-- How long a join token lives and whether a member ended it early, in milliseconds of the ledger's clock, in the
-- table a store that has no installation database keeps its join grants in. The installation database's own
-- migration 0070 adds the same two columns for the stores that have one. A token with no expiry holds zero, and so
-- does a token nobody revoked.

ALTER TABLE join_grants ADD COLUMN expires_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE join_grants ADD COLUMN revoked_at BIGINT NOT NULL DEFAULT 0;
