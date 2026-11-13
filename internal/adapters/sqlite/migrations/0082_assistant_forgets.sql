-- What a worker still holds of a deleted conversation it ran (ADR-0024): the run lanes, whose messages and
-- records its journal keeps, as a JSON array. It holds no text, and stands until the worker says it removed
-- what those runs left, so deleting a conversation reaches every machine that ran it.
CREATE TABLE assistant_forgets (
  workspace_id    TEXT   NOT NULL,
  conversation_id TEXT   NOT NULL,
  worker_id       TEXT   NOT NULL,
  lanes           TEXT   NOT NULL,
  deleted_at      BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, conversation_id, worker_id)
);

CREATE INDEX assistant_forgets_worker ON assistant_forgets (workspace_id, worker_id, deleted_at);
