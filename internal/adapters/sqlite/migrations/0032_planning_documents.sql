-- Documents and their versions, plan versions, and the document versions a contract version cites.
-- A document is a spec, an architecture document, an experience design, a milestone, or a
-- requirements file. Its ID is its reference, `<kind>:<id>`, and each committed change to its file
-- is a version the ledger numbers.

CREATE TABLE documents (
  workspace_id TEXT NOT NULL,
  id           TEXT NOT NULL,
  kind         TEXT NOT NULL,
  plan_id      TEXT NOT NULL DEFAULT '',
  title        TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE INDEX documents_plan ON documents (workspace_id, plan_id);

CREATE TABLE document_versions (
  workspace_id TEXT   NOT NULL,
  document_id  TEXT   NOT NULL,
  version      BIGINT NOT NULL,
  title        TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  source       TEXT,
  body         TEXT   NOT NULL,
  fields       TEXT   NOT NULL,
  PRIMARY KEY (workspace_id, document_id, version)
);

CREATE TABLE plan_versions (
  workspace_id TEXT   NOT NULL,
  plan_id      TEXT   NOT NULL,
  version      BIGINT NOT NULL,
  digest       TEXT   NOT NULL,
  source       TEXT,
  documents    TEXT   NOT NULL,
  decisions    TEXT   NOT NULL,
  PRIMARY KEY (workspace_id, plan_id, version)
);

CREATE TABLE contract_documents (
  workspace_id     TEXT   NOT NULL,
  contract_id      TEXT   NOT NULL,
  contract_version BIGINT NOT NULL,
  position         BIGINT NOT NULL,
  document_id      TEXT   NOT NULL,
  document_version BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, contract_id, contract_version, document_id)
);

-- What a contract file says of where it sits among the others. Intake keeps it as written.
ALTER TABLE contracts ADD COLUMN milestone TEXT NOT NULL DEFAULT '';
ALTER TABLE contracts ADD COLUMN shared BIGINT NOT NULL DEFAULT 0;
ALTER TABLE contracts ADD COLUMN uses TEXT NOT NULL DEFAULT '[]';
