-- A worker's forge health report for a repository also names the workspace the repository's checkout is, when that
-- workspace is a git clone that does not ignore its folder for lane worktrees. It is empty for a report that names
-- none, and for a report from before this column.

ALTER TABLE forge_health ADD COLUMN unignored_workspace TEXT NOT NULL DEFAULT '';
