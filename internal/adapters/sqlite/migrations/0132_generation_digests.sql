-- What each generation of a lane, and of an assistant's session, did (FR-SLOG-1, FR-SLOG-2): its digest, kept
-- as the worker sent it. A digest is a record of its owner and generation, so the worker's upload of it
-- again after a crash changes nothing. An orchestrator's digest belongs to the installation, which keeps it
-- in a table of this name; a ledger that is also the installation's holds it here.
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
