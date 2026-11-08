-- What this worker told the ledger of an effect nothing can observe, and which of a person's words it
-- acted on. Each row is written with the event that says so, so a crash leaves both or neither.

CREATE TABLE effect_reports (
  key     TEXT   NOT NULL,
  attempt BIGINT NOT NULL,
  PRIMARY KEY (key, attempt)
);

CREATE TABLE effect_settlements (
  id TEXT PRIMARY KEY
);
