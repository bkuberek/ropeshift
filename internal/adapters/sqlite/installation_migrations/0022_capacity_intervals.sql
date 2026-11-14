-- Each interval in which a worker's running lanes stood at one of its lane caps, with the cap's kind and
-- value (FR-OBS-14). The control plane records it from the worker's reports, by its own clock less each
-- report's age: started_at is the first report that saw the cap, seen_at the last, and ended_at stays 0
-- while the interval stands. Reports are not kept, so this is the record of when a worker was full. A
-- report moves it with no event, as it replaces the worker's report.
CREATE TABLE capacity_intervals (
  workspace_id TEXT   NOT NULL,
  worker_id    TEXT   NOT NULL,
  cap          TEXT   NOT NULL,
  cap_value    BIGINT NOT NULL,
  epoch        BIGINT NOT NULL,
  started_at   BIGINT NOT NULL,
  seen_at      BIGINT NOT NULL,
  ended_at     BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, worker_id, cap, started_at),
  CONSTRAINT capacity_cap CHECK (cap IN ('max_lanes', 'max_lanes_per_sign_in'))
);

CREATE INDEX capacity_intervals_standing ON capacity_intervals (workspace_id, worker_id, ended_at);
CREATE INDEX capacity_intervals_end ON capacity_intervals (workspace_id, ended_at, seen_at);
