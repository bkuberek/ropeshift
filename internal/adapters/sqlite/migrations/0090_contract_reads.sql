-- The other repositories of its project a contract's lanes read, by name, beside those their dependencies
-- target (ADR-0022), as a JSON array.
ALTER TABLE contracts ADD COLUMN reads TEXT NOT NULL DEFAULT '[]';
