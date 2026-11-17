-- Whether a repository's record was made before its identity was the root reached by following first parents
-- (FR-REPO-14). Such a record is named by the lowest root the repository had, which is still one of the roots
-- of its clone, so a registration or a source read may match it by any root. A record made after is named by
-- its first-parent root, and no other root matches it.
--
-- The column defaults to legacy, and this build writes the value of every record it makes, so a record a build
-- from before the column inserts, without naming it, is legacy, as it is named by the lowest root. Every record
-- the ledger holds now is legacy.
ALTER TABLE repositories ADD COLUMN identity_legacy BIGINT NOT NULL DEFAULT 1;
