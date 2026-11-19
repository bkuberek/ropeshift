-- A template write request's name (FR-TPL-1), as the installation database holds it (installation migration 0085). A
-- store with no installation keeps it here, under its own workspace, with the same column and statements.
ALTER TABLE template_writes ADD COLUMN name TEXT NOT NULL DEFAULT '';
