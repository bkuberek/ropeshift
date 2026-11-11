-- The installation database (ADR-0021): the registry of projects, the installation's own event log, the
-- repository index, and the records every project of the installation shares. Each project's ledger is a
-- database of its own beside this one.

-- The one row that holds the revision of the installation's event log.
CREATE TABLE installation (
  id  BIGINT NOT NULL PRIMARY KEY,
  rev BIGINT NOT NULL,
  CONSTRAINT installation_is_one CHECK (id = 1)
);

INSERT INTO installation (id, rev) VALUES (1, 0);

-- A project's ID is the workspace ID every record of its ledger carries, and database names where that
-- ledger lives: a file relative to the state directory, or a schema on a server.
CREATE TABLE projects (
  id           TEXT   NOT NULL PRIMARY KEY,
  name         TEXT   NOT NULL,
  display_name TEXT   NOT NULL,
  state        TEXT   NOT NULL,
  database     TEXT   NOT NULL,
  created_at   BIGINT NOT NULL,
  CONSTRAINT project_name UNIQUE (name),
  CONSTRAINT project_database UNIQUE (database),
  CONSTRAINT project_state CHECK (state IN ('active', 'archived'))
);

-- Changes to the records of this database, in the shape of a project's events.
CREATE TABLE installation_events (
  rev          BIGINT NOT NULL PRIMARY KEY,
  id           TEXT   NOT NULL,
  type         TEXT   NOT NULL,
  actor_kind   TEXT   NOT NULL,
  actor_id     TEXT   NOT NULL,
  subject_kind TEXT   NOT NULL,
  subject_id   TEXT   NOT NULL,
  data         TEXT   NOT NULL,
  occurred_at  BIGINT,
  recorded_at  BIGINT NOT NULL,
  traceparent  TEXT,
  CONSTRAINT installation_event_id UNIQUE (id)
);

-- Which project holds each repository, by the repository's identity. It is routing data the daemon
-- rebuilds from every project's repository records at start, not a copy of them.
CREATE TABLE repository_index (
  identity      TEXT NOT NULL PRIMARY KEY,
  project_id    TEXT NOT NULL,
  repository_id TEXT NOT NULL
);

-- The shared records keep the columns a single ledger gave them, so a project's Store reads and writes
-- them with the same statements. Their workspace_id is always the installation's own scope.

CREATE TABLE members (
  workspace_id     TEXT NOT NULL,
  id               TEXT NOT NULL,
  identity_subject TEXT NOT NULL,
  display_name     TEXT NOT NULL,
  status           TEXT NOT NULL,
  proof_digest     TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE member_sessions (
  workspace_id TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  member_id    TEXT   NOT NULL,
  created_at   BIGINT NOT NULL,
  expires_at   BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, digest)
);

CREATE INDEX member_sessions_expiry ON member_sessions (workspace_id, expires_at);

CREATE TABLE join_grants (
  workspace_id TEXT NOT NULL,
  id           TEXT NOT NULL,
  digest       TEXT NOT NULL,
  created_by   TEXT NOT NULL,
  used_by      TEXT,
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE workers (
  workspace_id      TEXT   NOT NULL,
  id                TEXT   NOT NULL,
  name              TEXT   NOT NULL,
  epoch             BIGINT NOT NULL,
  protocol          BIGINT NOT NULL,
  capabilities      TEXT   NOT NULL,
  proof_digest      TEXT   NOT NULL,
  last_handshake_at BIGINT,
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE orchestrators (
  workspace_id  TEXT   NOT NULL,
  id            TEXT   NOT NULL,
  worker_id     TEXT   NOT NULL,
  desired_state TEXT   NOT NULL,
  run           BIGINT NOT NULL DEFAULT 0,
  resets        BIGINT NOT NULL DEFAULT 0,
  actual_state  TEXT   NOT NULL DEFAULT '',
  generation    BIGINT NOT NULL DEFAULT 0,
  conditions    TEXT   NOT NULL DEFAULT '',
  reported_at   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, worker_id),
  CONSTRAINT orchestrator_desired CHECK (desired_state IN ('running', 'stopped'))
);

CREATE TABLE worker_autonomy (
  workspace_id        TEXT   NOT NULL,
  worker_id           TEXT   NOT NULL,
  level               TEXT   NOT NULL,
  acknowledged_by     TEXT   NOT NULL,
  acknowledged_kind   TEXT   NOT NULL,
  acknowledged_digest TEXT   NOT NULL,
  acknowledged_at     BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, worker_id),
  CONSTRAINT worker_autonomy_level CHECK (level IN ('', 'manual', 'attended', 'unattended')),
  CONSTRAINT worker_autonomy_acknowledger CHECK (acknowledged_by = '' OR acknowledged_kind = 'member')
);

CREATE TABLE run_windows (
  workspace_id TEXT   NOT NULL,
  id           TEXT   NOT NULL,
  worker_id    TEXT   NOT NULL,
  opened_by    TEXT   NOT NULL,
  opened_kind  TEXT   NOT NULL,
  opened_at    BIGINT NOT NULL,
  closes_at    BIGINT NOT NULL,
  closed_by    TEXT   NOT NULL,
  closed_at    BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT run_window_opener CHECK (opened_kind = 'member'),
  CONSTRAINT run_window_has_an_end CHECK (closes_at > opened_at)
);

CREATE INDEX run_windows_worker ON run_windows (workspace_id, worker_id, closes_at);

-- Only the autonomy policy lives here, because it governs workers, which serve every project. A
-- project's own policies stay in its ledger.
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
  PRIMARY KEY (workspace_id, kind, version),
  CONSTRAINT policy_is_autonomy CHECK (kind = 'autonomy')
);
