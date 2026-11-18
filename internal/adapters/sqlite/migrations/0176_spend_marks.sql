-- What an owner's generation was last charged in a scope of spend (ADR-0037): the highest figure its worker reported. An
-- assistant's run reports what a generation has spent as a running figure, so the day's tally adds the difference between a
-- report and the mark, and a repeated or smaller report adds nothing. The owner is the run's lane, and a mark only grows.
CREATE TABLE spend_marks (
  workspace_id TEXT             NOT NULL,
  scope        TEXT             NOT NULL,
  owner        TEXT             NOT NULL,
  generation   BIGINT           NOT NULL,
  charged_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, scope, owner, generation),
  CONSTRAINT spend_mark_not_negative CHECK (charged_usd >= 0)
);
