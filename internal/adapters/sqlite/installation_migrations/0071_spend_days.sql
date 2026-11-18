-- What each machine's orchestrator was charged, by UTC day (ADR-0037, FR-SPEND-6). The orchestrator serves every project
-- of its machine, so its tally is the installation's, and its soft daily limit reads it. The tables are a project's own,
-- spend_days and spend_marks, with the scope naming the machine, so one set of statements keeps both. Day is the first
-- millisecond of the UTC day on the ledger's clock, and a row only grows.
CREATE TABLE spend_days (
  workspace_id TEXT             NOT NULL,
  scope        TEXT             NOT NULL,
  day          BIGINT           NOT NULL,
  charged_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, scope, day),
  CONSTRAINT spend_day_not_negative CHECK (charged_usd >= 0)
);

-- What an orchestrator's generation was last charged in a scope: the highest figure its worker reported, so the day's
-- tally adds the difference between a report and the mark, and a repeated or smaller report adds nothing.
CREATE TABLE spend_marks (
  workspace_id TEXT             NOT NULL,
  scope        TEXT             NOT NULL,
  owner        TEXT             NOT NULL,
  generation   BIGINT           NOT NULL,
  charged_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, scope, owner, generation),
  CONSTRAINT spend_mark_not_negative CHECK (charged_usd >= 0)
);
