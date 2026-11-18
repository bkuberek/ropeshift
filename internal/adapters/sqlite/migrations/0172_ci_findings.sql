-- What CI's classification found of a finished head that no agent can act on (ADR-0037): an infrastructure conclusion
-- that stayed after the automatic reruns, a failure the base branch's tip shares, or a check that waits for a person
-- on the forge. One row per head and kind. It names the check and its conclusion and holds no log.
CREATE TABLE ci_findings (
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
  CONSTRAINT ci_finding_kind CHECK (kind IN ('ci_infrastructure_failure', 'ci_base_red', 'ci_action_required'))
);
