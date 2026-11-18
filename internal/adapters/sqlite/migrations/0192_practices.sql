-- What the practices check records (FR-PRAC-1). A request asks the worker that holds a repository's checkout to run the
-- check: it is offered until that worker reports, and one nobody reported by its expiry is stored as expired, while a
-- report that comes later is still recorded. practices_results holds the latest result of each repository, as the JSON
-- the worker reported, and practices_scans the commit the last completed secret scan of each repository read and the
-- version of the credential rule set it matched with, from which the next scan starts only when that version is the
-- one in force.

CREATE TABLE practices_requests (
  workspace_id  TEXT   NOT NULL,
  id            TEXT   NOT NULL,
  repository_id TEXT   NOT NULL,
  worker        TEXT   NOT NULL,
  cause         TEXT   NOT NULL,
  requested_by  TEXT   NOT NULL,
  requested_at  BIGINT NOT NULL,
  expires_at    BIGINT NOT NULL,
  state         TEXT   NOT NULL,
  reported_at   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT practices_request_cause CHECK (cause IN ('registration', 'planning_session', 'member')),
  CONSTRAINT practices_request_state CHECK (state IN ('offered', 'reported', 'expired'))
);

CREATE INDEX practices_requests_worker ON practices_requests (workspace_id, worker, state, requested_at);
CREATE INDEX practices_requests_repository ON practices_requests (workspace_id, repository_id, state, requested_at);

CREATE TABLE practices_results (
  workspace_id  TEXT   NOT NULL,
  repository_id TEXT   NOT NULL,
  request_id    TEXT   NOT NULL,
  commit_digest TEXT   NOT NULL,
  result        TEXT   NOT NULL,
  checked_at    BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, repository_id)
);

CREATE TABLE practices_scans (
  workspace_id  TEXT NOT NULL,
  repository_id TEXT NOT NULL,
  scanned_tip   TEXT   NOT NULL,
  rules_version BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, repository_id)
);
