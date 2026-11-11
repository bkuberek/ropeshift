-- The repository a project plans in, its plan of record (ADR-0022). A member names at most one per project,
-- and intake then reads plan files only from it.
ALTER TABLE repositories ADD COLUMN planning BIGINT NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX repository_planning ON repositories (workspace_id) WHERE planning <> 0;
