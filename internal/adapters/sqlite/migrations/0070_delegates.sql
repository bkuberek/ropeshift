-- Delegate credentials for a store with no installation database, which keeps the records every project
-- shares in its own ledger under one workspace (ADR-0021, ADR-0024).
CREATE TABLE delegate_credentials (
  workspace_id TEXT   NOT NULL,
  id           TEXT   NOT NULL,
  digest       TEXT   NOT NULL,
  member_id    TEXT   NOT NULL,
  project_id   TEXT   NOT NULL,
  label        TEXT   NOT NULL,
  kind         TEXT   NOT NULL,
  created_at   BIGINT NOT NULL,
  expires_at   BIGINT NOT NULL,
  last_used_at BIGINT,
  revoked_at   BIGINT,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT delegate_life CHECK (expires_at > created_at),
  CONSTRAINT delegate_label CHECK (label <> '')
);

CREATE UNIQUE INDEX delegate_credentials_digest ON delegate_credentials (workspace_id, digest);
CREATE INDEX delegate_credentials_member ON delegate_credentials (workspace_id, member_id);

-- A confirmation card is an approval request of kind confirm (ADR-0024): an operation a delegate called,
-- which runs only when a member confirms it. It belongs to no lane and no work order, and only a member
-- decides it. A delegate decides a request the delegation policy covers, as an orchestrator does, and a
-- card whose operation the API refused when it ran is failed, with its decider kept.
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
  CONSTRAINT approval_decider_kind CHECK (decided_by IS NULL OR decided_kind IN ('member', 'orchestrator', 'delegate')),
  CONSTRAINT approval_decider_name CHECK (decided_by IS NULL OR decided_by LIKE decided_kind || ':%'),
  CONSTRAINT approval_decided_digest CHECK (decided_by IS NULL OR decided_digest = subject_digest),
  CONSTRAINT approval_has_a_decider CHECK (state NOT IN ('approved', 'used', 'failed') OR decided_by IS NOT NULL),
  CONSTRAINT approval_names_its_lane CHECK (kind IN ('plan', 'recovery', 'merge', 'confirm') OR (lane_id IS NOT NULL AND work_order_id IS NOT NULL AND generation IS NOT NULL)),
  CONSTRAINT approval_plan_is_a_members CHECK (kind <> 'plan' OR decided_by IS NULL OR decided_kind = 'member'),
  CONSTRAINT approval_plan_only CHECK (kind = 'plan' OR (plan_id IS NULL AND plan_version IS NULL)),
  CONSTRAINT approval_recovery_names_its_work CHECK (kind <> 'recovery' OR work_order_id IS NOT NULL),
  CONSTRAINT approval_recovery_is_a_members CHECK (kind <> 'recovery' OR decided_by IS NULL OR decided_kind = 'member'),
  CONSTRAINT approval_merge_names_its_work CHECK (kind <> 'merge' OR (work_order_id IS NOT NULL AND lane_id IS NULL)),
  CONSTRAINT approval_merge_is_a_members CHECK (kind <> 'merge' OR decided_by IS NULL OR decided_kind = 'member'),
  CONSTRAINT approval_confirm_names_no_lane CHECK (kind <> 'confirm' OR (lane_id IS NULL AND work_order_id IS NULL AND generation IS NULL)),
  CONSTRAINT approval_confirm_is_a_members CHECK (kind <> 'confirm' OR decided_by IS NULL OR decided_kind = 'member')
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
