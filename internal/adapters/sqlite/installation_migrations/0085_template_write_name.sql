-- A template write request may name the project (FR-TPL-1): the name the worker renders into the files and into the
-- commands that register them, in place of the folder's name. It is empty for a request that names none, which keeps
-- the folder's name. It keeps the columns a project's ledger gives it, so a store with no installation reads and writes
-- it with the same statements.
ALTER TABLE template_writes ADD COLUMN name TEXT NOT NULL DEFAULT '';
