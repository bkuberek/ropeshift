-- Questions, decisions, the categories a workspace adds, and the text search reads. How a
-- database indexes that text is its own migration.

CREATE TABLE questions (
  workspace_id      TEXT   NOT NULL,
  id                TEXT   NOT NULL,
  text              TEXT   NOT NULL,
  normalized_hash   TEXT   NOT NULL,
  categories        TEXT   NOT NULL,
  status            TEXT   NOT NULL,
  rung              BIGINT NOT NULL,
  blocking          BIGINT NOT NULL,
  subject_kind      TEXT,
  subject_id        TEXT,
  batch_id          TEXT,
  asked_by          TEXT,
  options           TEXT   NOT NULL,
  recommendation    TEXT,
  escalation_reason TEXT,
  ladder_trace      TEXT   NOT NULL,
  claimed_by        TEXT,
  claim_until       BIGINT,
  decision_id       TEXT,
  PRIMARY KEY (workspace_id, id)
);

CREATE INDEX questions_hash ON questions (workspace_id, normalized_hash);
CREATE INDEX questions_subject ON questions (workspace_id, subject_kind, subject_id);

CREATE TABLE question_attachments (
  workspace_id TEXT   NOT NULL,
  question_id  TEXT   NOT NULL,
  lane_id      TEXT   NOT NULL,
  generation   BIGINT NOT NULL,
  blocking     BIGINT NOT NULL,
  attached_at  BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, question_id, lane_id)
);

CREATE TABLE decisions (
  workspace_id   TEXT   NOT NULL,
  id             TEXT   NOT NULL,
  status         TEXT   NOT NULL,
  statement      TEXT   NOT NULL,
  rationale      TEXT,
  citations      TEXT   NOT NULL,
  scope          TEXT,
  reversibility  TEXT,
  categories     TEXT   NOT NULL,
  decided_by     TEXT,
  supersedes     TEXT,
  question_id    TEXT,
  policy_version BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE workspace_categories (
  workspace_id TEXT NOT NULL,
  name         TEXT NOT NULL,
  PRIMARY KEY (workspace_id, name)
);

CREATE TABLE search_documents (
  workspace_id TEXT NOT NULL,
  kind         TEXT NOT NULL,
  record_id    TEXT NOT NULL,
  body         TEXT NOT NULL,
  PRIMARY KEY (workspace_id, kind, record_id)
);
