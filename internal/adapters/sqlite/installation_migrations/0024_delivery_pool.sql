-- The installation's delivery pool (FR-EST-13): the delivery records of every project whose estimate policy
-- shares its history, which only such projects draw on, at their broadest tier. A row holds what an estimate
-- matches and draws on, and no more: kind, size, capability class, the four durations, the cost and whether it
-- was a lower bound, the outcome, and when the work ended. It names no work order, repository, agent, or
-- person. project_id is the project that shared it, so the project can replace or take out its share; a
-- project's reads never return it. A project's share is derived from its delivery records, so it changes with
-- no event.
CREATE TABLE delivery_pool (
  workspace_id     TEXT             NOT NULL,
  project_id       TEXT             NOT NULL,
  seq              BIGINT           NOT NULL,
  kind             TEXT             NOT NULL,
  size             TEXT             NOT NULL,
  capability_class TEXT             NOT NULL,
  outcome          TEXT             NOT NULL,
  ended_at         BIGINT           NOT NULL,
  elapsed_ms       BIGINT           NOT NULL,
  delivery_ms      BIGINT           NOT NULL,
  lane_ms          BIGINT           NOT NULL,
  person_ms        BIGINT           NOT NULL,
  cost_usd         DOUBLE PRECISION NOT NULL,
  cost_lower_bound BIGINT           NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, project_id, seq)
);

CREATE INDEX delivery_pool_ended ON delivery_pool (workspace_id, ended_at);

-- What each sharing project shares: the digest of its records in the pool, so it rewrites its share only when
-- that changed.
CREATE TABLE delivery_pool_shares (
  workspace_id TEXT   NOT NULL,
  project_id   TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  shared_at    BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, project_id)
);
