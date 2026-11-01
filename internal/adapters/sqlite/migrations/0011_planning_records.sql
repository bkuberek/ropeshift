-- Goals and requirement versions, which intake reads from repository files.

CREATE TABLE goals (
  workspace_id     TEXT NOT NULL,
  id               TEXT NOT NULL,
  title            TEXT NOT NULL,
  status           TEXT NOT NULL,
  success_measures TEXT NOT NULL,
  statement        TEXT NOT NULL,
  source           TEXT,
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE requirements (
  workspace_id    TEXT   NOT NULL,
  id              TEXT   NOT NULL,
  version         BIGINT NOT NULL,
  requirement_key TEXT   NOT NULL,
  text            TEXT   NOT NULL,
  source          TEXT,
  digest          TEXT   NOT NULL,
  PRIMARY KEY (workspace_id, id, version)
);

CREATE INDEX requirements_key ON requirements (workspace_id, requirement_key);
