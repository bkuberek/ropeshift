-- The security notes a review lane left on the work it judged (FR-PRAC-9). A note is bound to the commit the
-- review read and to the attempt that left it, as its verdicts are, and seq is its place in the review's report.
-- reasons and citations are redacted of credentials before they are kept, and redaction_rules_version is the version
-- of the rule set that redacted them. A note is shown beside the verdicts and posted with them, and no decision reads it.

CREATE TABLE review_notes (
  workspace_id            TEXT   NOT NULL,
  review_id               TEXT   NOT NULL,
  work_order_id           TEXT   NOT NULL,
  attempt                 BIGINT NOT NULL,
  commit_digest           TEXT   NOT NULL,
  seq                     BIGINT NOT NULL,
  point                   TEXT   NOT NULL,
  reasons                 TEXT   NOT NULL,
  citations               TEXT   NOT NULL,
  redaction_rules_version BIGINT NOT NULL,
  created_at              BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, review_id, seq),
  CONSTRAINT review_note_point CHECK (point IN ('secrets', 'injection', 'dependencies', 'authority')),
  CONSTRAINT review_note_seq CHECK (seq >= 0),
  CONSTRAINT review_note_redaction CHECK (redaction_rules_version >= 1)
);

CREATE INDEX review_notes_work ON review_notes (workspace_id, work_order_id, attempt);
