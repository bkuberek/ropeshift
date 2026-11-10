-- The workspace's record of each repository. Git gives the remote and the default branch when a source is
-- read, and a member gives the rest: the base branch lanes start from (empty follows the default
-- branch), the forge binding, how a pull request is merged, whether ropeshift integrates accepted work,
-- and what work that depends on other work starts from. Accepted work cannot be built on when the merge
-- method rewrites commits, which the table holds as well as the code.

CREATE TABLE repositories (
  workspace_id   TEXT   NOT NULL,
  id             TEXT   NOT NULL,
  remote         TEXT   NOT NULL DEFAULT '',
  default_branch TEXT   NOT NULL DEFAULT '',
  base_branch    TEXT   NOT NULL DEFAULT '',
  forge_binding  TEXT   NOT NULL DEFAULT '',
  merge_method   TEXT   NOT NULL DEFAULT 'merge',
  integration    BIGINT NOT NULL DEFAULT 0,
  start_from     TEXT   NOT NULL DEFAULT 'merged',
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT repository_merge_method CHECK (merge_method IN ('merge', 'squash', 'rebase')),
  CONSTRAINT repository_start_from CHECK (start_from IN ('merged', 'accepted')),
  CONSTRAINT repository_commits_survive CHECK (start_from <> 'accepted' OR merge_method = 'merge')
);

-- The branch a work order names to start from, which overrides its repository's base branch.
ALTER TABLE work_orders ADD COLUMN base TEXT;
