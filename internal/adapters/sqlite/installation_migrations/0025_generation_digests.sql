-- What each generation of an orchestrator did (FR-SLOG-1, FR-SLOG-2): its digest, kept as the worker sent it.
-- An orchestrator serves every project on its machine, so its digest is an installation record, and the
-- installation's backup carries it. The worker's upload of a digest again after a crash changes nothing.
CREATE TABLE generation_digests (
  workspace_id TEXT   NOT NULL,
  owner_kind   TEXT   NOT NULL,
  owner_id     TEXT   NOT NULL,
  generation   BIGINT NOT NULL,
  body         TEXT   NOT NULL,
  recorded_at  BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, owner_kind, owner_id, generation),
  CONSTRAINT generation_digest_owner CHECK (owner_kind IN ('lane', 'assistant', 'orchestrator'))
);
