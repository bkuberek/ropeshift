-- What members did about one head's CI wait (ADR-0037): the time Wait longer added to the CI timeout, and the reruns
-- of the head, a member's and the automatic ones, counted together, with when the last one was asked for, from which
-- the head's CI clock runs again. A head nobody acted on has no row.
CREATE TABLE ci_waits (
  workspace_id  TEXT   NOT NULL,
  work_order_id TEXT   NOT NULL,
  head_commit   TEXT   NOT NULL,
  extended_ms   BIGINT NOT NULL DEFAULT 0,
  reruns        BIGINT NOT NULL DEFAULT 0,
  rerun_at      BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, work_order_id, head_commit),
  CONSTRAINT ci_wait_counts CHECK (extended_ms >= 0 AND reruns >= 0)
);
