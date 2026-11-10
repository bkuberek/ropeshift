-- Plan approvals. A plan version's digest covers every record that names the plan, so a version keeps
-- the file's own digest, the records it rests on, and the `plan` work order whose branch held it. A
-- plan approval is an approval request of kind `plan`: it belongs to a plan and not to a lane, so the
-- lane, the work order, and the generation may be empty for that kind and for no other. Only a member
-- decides one, which the table holds as well as the code. The table is made again, because a column
-- cannot lose its NOT NULL in place in every database.

ALTER TABLE plan_versions ADD COLUMN file_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE plan_versions ADD COLUMN records TEXT NOT NULL DEFAULT '[]';
ALTER TABLE plan_versions ADD COLUMN work_order TEXT NOT NULL DEFAULT '';

UPDATE plan_versions SET file_digest = digest;

-- The work orders a contract version lists, with a digest of each, for a plan's difference.
ALTER TABLE contracts ADD COLUMN work_orders TEXT NOT NULL DEFAULT '[]';

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
  CONSTRAINT approval_names_its_lane CHECK (kind = 'plan' OR (lane_id IS NOT NULL AND work_order_id IS NOT NULL AND generation IS NOT NULL)),
  CONSTRAINT approval_plan_is_a_members CHECK (kind <> 'plan' OR decided_by IS NULL OR decided_kind = 'member'),
  CONSTRAINT approval_plan_only CHECK (kind = 'plan' OR (plan_id IS NULL AND plan_version IS NULL))
);

INSERT INTO approval_requests_next
  (workspace_id, id, lane_id, work_order_id, generation, kind, subject, subject_digest, input, command, path, summary,
   requested_by, state, decided_by, decided_kind, decided_digest, decided_at, reason, policy_version, raised_at, expires_at,
   channel, used_by)
SELECT
   workspace_id, id, lane_id, work_order_id, generation, kind, subject, subject_digest, input, command, path, summary,
   requested_by, state, decided_by, decided_kind, decided_digest, decided_at, reason, policy_version, raised_at, expires_at,
   channel, used_by
FROM approval_requests;

DROP TABLE approval_requests;

ALTER TABLE approval_requests_next RENAME TO approval_requests;

CREATE INDEX approval_requests_lane ON approval_requests (workspace_id, lane_id);
CREATE INDEX approval_requests_work_order ON approval_requests (workspace_id, work_order_id);
CREATE INDEX approval_requests_state ON approval_requests (workspace_id, state);
CREATE INDEX approval_requests_plan ON approval_requests (workspace_id, plan_id);
