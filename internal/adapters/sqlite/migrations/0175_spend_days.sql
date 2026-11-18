-- What a project's lanes were charged, by UTC day (ADR-0037, FR-SPEND-6). A project's daily and weekly limits read it:
-- the day is one row and the week is the sum of the days from Monday. A scope keeps one kind of spend apart from
-- another, the lanes of work orders from an assistant, because only the first holds a project back. Day is the
-- first millisecond of the UTC day on the ledger's clock, and a row only grows.
CREATE TABLE spend_days (
  workspace_id TEXT             NOT NULL,
  scope        TEXT             NOT NULL,
  day          BIGINT           NOT NULL,
  charged_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, scope, day),
  CONSTRAINT spend_day_not_negative CHECK (charged_usd >= 0)
);
