-- What each head's required checks took (FR-SPEND-21): one record when they all finished, per repository, head, and
-- attempt. A repository's p50 and p85 are read from its recent records that have a test result, and the CI read
-- schedule runs by them. Times are database milliseconds, and result is the class of the finished head.
CREATE TABLE ci_records (
  workspace_id   TEXT   NOT NULL,
  repository_id  TEXT   NOT NULL,
  head_commit    TEXT   NOT NULL,
  attempt        BIGINT NOT NULL,
  pushed_at      BIGINT NOT NULL,
  first_start    BIGINT NOT NULL,
  last_completed BIGINT NOT NULL,
  queue_ms       BIGINT NOT NULL,
  duration_ms    BIGINT NOT NULL,
  result         TEXT   NOT NULL,
  PRIMARY KEY (workspace_id, repository_id, head_commit, attempt),
  CONSTRAINT ci_record_result CHECK (result IN ('pass', 'failure', 'infrastructure', 'action_required'))
);

CREATE INDEX ci_records_recent ON ci_records (workspace_id, repository_id, last_completed);

-- When the ledger first held a pull request's head, which a CI record dates the push by. A row from before this
-- holds 0, and a CI record then dates the push by the first start of its checks.
ALTER TABLE pull_requests ADD COLUMN head_since BIGINT NOT NULL DEFAULT 0;
