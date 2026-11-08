-- What reaches members together. At most one batch gathers at a time, and a delivered one
-- counts once toward interruptions.

CREATE TABLE question_batches (
  workspace_id TEXT   NOT NULL,
  id           TEXT   NOT NULL,
  status       TEXT   NOT NULL,
  opened_at    BIGINT NOT NULL,
  closes_at    BIGINT NOT NULL,
  delivered_at BIGINT,
  PRIMARY KEY (workspace_id, id)
);

CREATE UNIQUE INDEX question_batches_gathering ON question_batches (workspace_id) WHERE status = 'open';
CREATE INDEX questions_batch ON questions (workspace_id, batch_id);
