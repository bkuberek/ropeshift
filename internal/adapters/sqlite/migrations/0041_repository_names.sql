-- What commands and a work order's `repo` call each repository, unique in its project (ADR-0022). A record
-- made before names existed has none until a source reads it again, so an empty name is not held unique.
ALTER TABLE repositories ADD COLUMN name TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX repository_name ON repositories (workspace_id, name) WHERE name <> '';

-- The name a contract's `repo` gave that no repository of the project held when intake recorded the work.
-- The work fails the definition of ready until a repository takes that name, and then targets it.
ALTER TABLE work_orders ADD COLUMN unknown_repository TEXT;
