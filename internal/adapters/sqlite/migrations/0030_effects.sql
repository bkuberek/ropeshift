-- What a person can read and decide of an effect a crash left unknown and nothing can observe. The
-- effect itself lives in a worker's journal. The check is the rule itself, so that no code path can
-- get past it: only a member decides.

CREATE TABLE unknown_effects (
  workspace_id  TEXT   NOT NULL,
  id            TEXT   NOT NULL,
  worker_id     TEXT   NOT NULL,
  effect_key    TEXT   NOT NULL,
  kind          TEXT   NOT NULL,
  lane_id       TEXT   NOT NULL,
  work_order_id TEXT   NOT NULL,
  generation    BIGINT NOT NULL,
  target        TEXT   NOT NULL,
  attempt       BIGINT NOT NULL,
  raised_at     BIGINT NOT NULL,
  outcome       TEXT   NOT NULL DEFAULT '',
  reason        TEXT   NOT NULL DEFAULT '',
  decided_by    TEXT   NOT NULL DEFAULT '',
  decided_kind  TEXT   NOT NULL DEFAULT '',
  decided_at    BIGINT NOT NULL DEFAULT 0,
  settled_at    BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT unknown_effect_outcome CHECK (outcome IN ('', 'happened', 'not_happened', 'retry')),
  CONSTRAINT unknown_effect_decider CHECK (outcome = '' OR (decided_kind = 'member' AND decided_by <> '')),
  CONSTRAINT unknown_effect_settles_after_a_decision CHECK (settled_at = 0 OR outcome <> '')
);

CREATE UNIQUE INDEX unknown_effects_attempt ON unknown_effects (workspace_id, worker_id, effect_key, attempt);
CREATE INDEX unknown_effects_undelivered ON unknown_effects (workspace_id, worker_id, settled_at);

-- A read model says how long something has waited, which only the log knows.
CREATE INDEX events_subject ON events (workspace_id, subject_kind, subject_id, rev);
