-- Recovery acts (FR-ORCH-8, ADR-0014). An orchestrator's act outside the recovery policy raises an
-- approval request of kind `recovery`, which belongs to a work order and, for a lane act, to that work
-- order's lane, so the lane and the generation may be empty for it too. Only a member decides one, which
-- the table holds as well as the code. The table is made again, because a check cannot change in place in
-- every database.

CREATE TABLE approval_requests_next (
  workspace_id   TEXT   NOT NULL,
  id             TEXT   NOT NULL,
  lane_id        TEXT,
  work_order_id  TEXT,
  generation     BIGINT,
  kind           TEXT   NOT NULL,
  subject        TEXT   NOT NULL,
  subject_digest TEXT   NOT NULL,
  input          TEXT   NOT NULL,
  command        TEXT   NOT NULL,
  path           TEXT   NOT NULL,
  summary        TEXT   NOT NULL,
  requested_by   TEXT   NOT NULL,
  state          TEXT   NOT NULL,
  decided_by     TEXT,
  decided_kind   TEXT,
  decided_digest TEXT,
  decided_at     BIGINT,
  reason         TEXT   NOT NULL,
  policy_version BIGINT NOT NULL,
  raised_at      BIGINT NOT NULL,
  expires_at     BIGINT NOT NULL,
  channel        TEXT   NOT NULL,
  used_by        TEXT   NOT NULL,
  plan_id        TEXT,
  plan_version   BIGINT,
  detail         TEXT   NOT NULL DEFAULT '',
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT approval_no_self_decision CHECK (decided_by IS NULL OR decided_by <> requested_by),
  CONSTRAINT approval_decider_kind CHECK (decided_by IS NULL OR decided_kind IN ('member', 'orchestrator')),
  CONSTRAINT approval_decider_name CHECK (decided_by IS NULL OR decided_by LIKE decided_kind || ':%'),
  CONSTRAINT approval_decided_digest CHECK (decided_by IS NULL OR decided_digest = subject_digest),
  CONSTRAINT approval_has_a_decider CHECK (state NOT IN ('approved', 'used') OR decided_by IS NOT NULL),
  CONSTRAINT approval_names_its_lane CHECK (kind IN ('plan', 'recovery') OR (lane_id IS NOT NULL AND work_order_id IS NOT NULL AND generation IS NOT NULL)),
  CONSTRAINT approval_plan_is_a_members CHECK (kind <> 'plan' OR decided_by IS NULL OR decided_kind = 'member'),
  CONSTRAINT approval_plan_only CHECK (kind = 'plan' OR (plan_id IS NULL AND plan_version IS NULL)),
  CONSTRAINT approval_recovery_names_its_work CHECK (kind <> 'recovery' OR work_order_id IS NOT NULL),
  CONSTRAINT approval_recovery_is_a_members CHECK (kind <> 'recovery' OR decided_by IS NULL OR decided_kind = 'member')
);

INSERT INTO approval_requests_next
  (workspace_id, id, lane_id, work_order_id, generation, kind, subject, subject_digest, input, command, path, summary,
   requested_by, state, decided_by, decided_kind, decided_digest, decided_at, reason, policy_version, raised_at, expires_at,
   channel, used_by, plan_id, plan_version, detail)
SELECT
   workspace_id, id, lane_id, work_order_id, generation, kind, subject, subject_digest, input, command, path, summary,
   requested_by, state, decided_by, decided_kind, decided_digest, decided_at, reason, policy_version, raised_at, expires_at,
   channel, used_by, plan_id, plan_version, detail
FROM approval_requests;

DROP TABLE approval_requests;

ALTER TABLE approval_requests_next RENAME TO approval_requests;

CREATE INDEX approval_requests_lane ON approval_requests (workspace_id, lane_id);
CREATE INDEX approval_requests_work_order ON approval_requests (workspace_id, work_order_id);
CREATE INDEX approval_requests_state ON approval_requests (workspace_id, state);
CREATE INDEX approval_requests_plan ON approval_requests (workspace_id, plan_id);

-- Every recovery act that ran or was refused, which is what bounds an orchestrator's breaker resets in a
-- day. The event log holds the same record.
CREATE TABLE recovery_acts (
  workspace_id   TEXT   NOT NULL,
  id             TEXT   NOT NULL,
  act            TEXT   NOT NULL,
  work_order_id  TEXT   NOT NULL,
  lane_id        TEXT   NOT NULL,
  actor          TEXT   NOT NULL,
  generation     BIGINT NOT NULL,
  reason         TEXT   NOT NULL,
  policy_version BIGINT NOT NULL,
  result         TEXT   NOT NULL,
  check_failed   TEXT   NOT NULL,
  approval_id    TEXT   NOT NULL,
  recorded_at    BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT recovery_act_result CHECK (result IN ('done', 'refused'))
);

CREATE INDEX recovery_acts_work_order ON recovery_acts (workspace_id, work_order_id, act, recorded_at);
