-- Pull requests through a forge (ADR-0020). The control plane requests each forge operation of a worker
-- as a recorded request, keyed by the work order and the commit or the review, so a request made twice is
-- made once. The worker reports how each attempt came out, and a failed attempt stays requested with the
-- next attempt counted. The worker that ran a lane publishes its branch, and the worker that opened a pull
-- request reads it and comments on it.
CREATE TABLE forge_requests (
  workspace_id  TEXT   NOT NULL,
  id            TEXT   NOT NULL,
  kind          TEXT   NOT NULL,
  work_order_id TEXT   NOT NULL,
  repository_id TEXT   NOT NULL,
  worker_id     TEXT   NOT NULL,
  binding       TEXT   NOT NULL,
  branch        TEXT   NOT NULL DEFAULT '',
  base          TEXT   NOT NULL DEFAULT '',
  head_commit   TEXT   NOT NULL DEFAULT '',
  number        BIGINT NOT NULL DEFAULT 0,
  title         TEXT   NOT NULL DEFAULT '',
  body          TEXT   NOT NULL DEFAULT '',
  attempt       BIGINT NOT NULL,
  state         TEXT   NOT NULL,
  detail        TEXT   NOT NULL DEFAULT '',
  requested_at  BIGINT NOT NULL,
  reported_at   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT forge_request_kind CHECK (kind IN ('publish', 'comment')),
  CONSTRAINT forge_request_state CHECK (state IN ('requested', 'done')),
  CONSTRAINT forge_request_attempt CHECK (attempt >= 1)
);

CREATE INDEX forge_requests_worker ON forge_requests (workspace_id, worker_id, state);
CREATE INDEX forge_requests_work_order ON forge_requests (workspace_id, work_order_id);

-- The pull request each work order lands through, as a worker last read it. Checks are those on the head
-- commit, as JSON.
CREATE TABLE pull_requests (
  workspace_id  TEXT   NOT NULL,
  work_order_id TEXT   NOT NULL,
  repository_id TEXT   NOT NULL,
  worker_id     TEXT   NOT NULL,
  number        BIGINT NOT NULL,
  url           TEXT   NOT NULL,
  state         TEXT   NOT NULL,
  base          TEXT   NOT NULL,
  branch        TEXT   NOT NULL,
  head_commit   TEXT   NOT NULL,
  mergeable     TEXT   NOT NULL DEFAULT '',
  merge_commit  TEXT   NOT NULL DEFAULT '',
  merged_at     TEXT   NOT NULL DEFAULT '',
  checks        TEXT   NOT NULL DEFAULT '[]',
  read_at       BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, work_order_id),
  CONSTRAINT pull_request_state CHECK (state IN ('open', 'closed', 'merged'))
);

-- What each worker last said of its forge sign-in for a repository: a health state of that repository on
-- that worker, since checked_at, when it last changed.
CREATE TABLE forge_health (
  workspace_id  TEXT   NOT NULL,
  worker_id     TEXT   NOT NULL,
  repository_id TEXT   NOT NULL,
  state         TEXT   NOT NULL,
  detail        TEXT   NOT NULL DEFAULT '',
  checked_at    BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, worker_id, repository_id),
  CONSTRAINT forge_health_state CHECK (state IN ('ok', 'signed_out', 'missing'))
);
