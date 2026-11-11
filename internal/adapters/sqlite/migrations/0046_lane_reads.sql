-- The other repositories of its project a lane reads, read-only, each at the commit recorded when the lane
-- starts (ADR-0022). A planning lane reads every one at its base branch tip, and the worker checks each out
-- with no branch beside the lane's worktree.
ALTER TABLE claims ADD COLUMN reads TEXT NOT NULL DEFAULT '[]';
