-- Versions of a policy, what a decision says about the decisions it overlaps, what lanes are
-- told when a decision ends, and what a decision later turned out to be. A body is kept as
-- it was written, and its digest covers those bytes.

CREATE TABLE policy_versions (
  workspace_id TEXT   NOT NULL,
  kind         TEXT   NOT NULL,
  version      BIGINT NOT NULL,
  id           TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  body         TEXT   NOT NULL,
  settings     TEXT   NOT NULL,
  authored_by  TEXT   NOT NULL,
  created_at   BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, kind, version)
);

ALTER TABLE decisions ADD COLUMN overlapping TEXT NOT NULL DEFAULT '[]';
ALTER TABLE decisions ADD COLUMN no_conflict TEXT NOT NULL DEFAULT '[]';

CREATE INDEX decisions_status ON decisions (workspace_id, status);

CREATE TABLE decision_notices (
  workspace_id TEXT   NOT NULL,
  decision_id  TEXT   NOT NULL,
  lane_id      TEXT   NOT NULL,
  generation   BIGINT NOT NULL,
  replaced_by  TEXT   NOT NULL,
  noticed_at   BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, decision_id, lane_id)
);

CREATE TABLE experience_records (
  workspace_id        TEXT   NOT NULL,
  id                  TEXT   NOT NULL,
  subject_kind        TEXT   NOT NULL,
  subject_id          TEXT   NOT NULL,
  policy_version      BIGINT NOT NULL,
  settings            TEXT   NOT NULL,
  options_considered  TEXT   NOT NULL,
  chosen              TEXT   NOT NULL,
  outcome             TEXT,
  outcome_observed_at BIGINT,
  PRIMARY KEY (workspace_id, id)
);

CREATE UNIQUE INDEX experience_records_subject ON experience_records (workspace_id, subject_kind, subject_id);

-- A decision recorded before this migration can still be overturned, so it needs a record
-- for its outcome to fill.
INSERT INTO experience_records (workspace_id, id, subject_kind, subject_id, policy_version, settings, options_considered, chosen)
SELECT workspace_id, id, 'decision', id, policy_version, '{}', '[]', statement FROM decisions;
