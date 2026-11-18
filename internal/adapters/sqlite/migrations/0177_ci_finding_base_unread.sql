-- A finding may now say the base branch's checks could not be read on several reads of a pull request in a row, so nobody
-- can tell whether a failed check is the base's (ADR-0037). The table is made again, because a check cannot change in
-- place in every database.

CREATE TABLE ci_findings_next (
  workspace_id  TEXT   NOT NULL,
  work_order_id TEXT   NOT NULL,
  head_commit   TEXT   NOT NULL,
  kind          TEXT   NOT NULL,
  check_name    TEXT   NOT NULL DEFAULT '',
  conclusion    TEXT   NOT NULL DEFAULT '',
  url           TEXT   NOT NULL DEFAULT '',
  base_commit   TEXT   NOT NULL DEFAULT '',
  raised_at     BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, work_order_id, head_commit, kind),
  CONSTRAINT ci_finding_kind CHECK (kind IN ('ci_infrastructure_failure', 'ci_base_red', 'ci_base_unread', 'ci_action_required'))
);

INSERT INTO ci_findings_next (workspace_id, work_order_id, head_commit, kind, check_name, conclusion, url, base_commit, raised_at)
SELECT workspace_id, work_order_id, head_commit, kind, check_name, conclusion, url, base_commit, raised_at
FROM ci_findings;

DROP TABLE ci_findings;

ALTER TABLE ci_findings_next RENAME TO ci_findings;
