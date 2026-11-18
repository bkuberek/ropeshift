-- A forge request may now ask a worker to rerun the failed jobs of a head's runs, and to read the logs of the checks
-- that failed (ADR-0037). The tables are made again, because a check cannot change in place in every database.

CREATE TABLE forge_requests_next (
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
  method        TEXT   NOT NULL DEFAULT '',
  attempt       BIGINT NOT NULL,
  state         TEXT   NOT NULL,
  outcome       TEXT   NOT NULL DEFAULT '',
  detail        TEXT   NOT NULL DEFAULT '',
  requested_at  BIGINT NOT NULL,
  reported_at   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT forge_request_kind CHECK (kind IN ('publish', 'comment', 'merge', 'rerun', 'logs')),
  CONSTRAINT forge_request_state CHECK (state IN ('requested', 'done')),
  CONSTRAINT forge_request_attempt CHECK (attempt >= 1),
  CONSTRAINT forge_request_merge_method CHECK (kind <> 'merge' OR method IN ('merge', 'squash', 'rebase')),
  CONSTRAINT forge_request_outcome CHECK (outcome IN ('', 'merged', 'conflict', 'behind', 'stale_head', 'refused'))
);

INSERT INTO forge_requests_next
  (workspace_id, id, kind, work_order_id, repository_id, worker_id, binding, branch, base, head_commit, number, title, body,
   method, attempt, state, outcome, detail, requested_at, reported_at)
SELECT
   workspace_id, id, kind, work_order_id, repository_id, worker_id, binding, branch, base, head_commit, number, title, body,
   method, attempt, state, outcome, detail, requested_at, reported_at
FROM forge_requests;

DROP TABLE forge_requests;

ALTER TABLE forge_requests_next RENAME TO forge_requests;

CREATE INDEX forge_requests_worker ON forge_requests (workspace_id, worker_id, state);
CREATE INDEX forge_requests_work_order ON forge_requests (workspace_id, work_order_id);
