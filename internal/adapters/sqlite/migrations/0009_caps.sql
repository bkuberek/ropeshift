-- The caps a claim is held to, beside the work-in-progress limit.

ALTER TABLE workspaces ADD COLUMN repository_limit BIGINT NOT NULL DEFAULT 0;
ALTER TABLE workspaces ADD COLUMN worker_limit BIGINT NOT NULL DEFAULT 0;
