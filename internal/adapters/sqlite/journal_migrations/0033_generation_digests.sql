-- The digest of each generation a worker runs, kept while the generation runs and until the ledger has heard of
-- it (FR-SLOG-1, FR-SLOG-2). `state` is what the worker needs to go on building the digest after a restart.
-- `over` says the generation ended and `state` is final, and `recorded` says the event that carries the
-- digest to the ledger is in the journal. A generation that is over and not recorded is what a crash between
-- its end and its record leaves, and the next start records it.
CREATE TABLE generation_digests (
  lane       TEXT   NOT NULL,
  generation BIGINT NOT NULL,
  state      BLOB   NOT NULL,
  over       BIGINT NOT NULL DEFAULT 0,
  recorded   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (lane, generation)
);
