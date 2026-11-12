-- Merging accepted work (ADR-0020). A forge request may now ask a worker to merge a reviewed commit, with
-- the repository's merge method, and keeps what the merge came to: merged, or the forge's or git's refusal.
-- The tables are made again, because a check cannot change in place in every database.

CREATE TABLE forge_requests_next (
  workspace_id  TEXT   NOT NULL,
  id            TEXT   NOT NULL,
  kind          TEXT   NOT NULL,
  work_order_id TEXT   NOT NULL,
  repository_id TEXT   NOT NULL,
  worker_id     TEXT   NOT NULL,
  binding       TEXT   NOT NULL,
  branch        TEXT   NOT NULL DEFAULT '',
  base          TEXT   NOT NULL DEFAULT '',
  head_commit   TEXT   NOT NULL DEFAULT '',
  number        BIGINT NOT NULL DEFAULT 0,
  title         TEXT   NOT NULL DEFAULT '',
  body          TEXT   NOT NULL DEFAULT '',
  method        TEXT   NOT NULL DEFAULT '',
  attempt       BIGINT NOT NULL,
  state         TEXT   NOT NULL,
  outcome       TEXT   NOT NULL DEFAULT '',
  detail        TEXT   NOT NULL DEFAULT '',
  requested_at  BIGINT NOT NULL,
  reported_at   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT forge_request_kind CHECK (kind IN ('publish', 'comment', 'merge')),
  CONSTRAINT forge_request_state CHECK (state IN ('requested', 'done')),
  CONSTRAINT forge_request_attempt CHECK (attempt >= 1),
  CONSTRAINT forge_request_merge_method CHECK (kind <> 'merge' OR method IN ('merge', 'squash', 'rebase')),
  CONSTRAINT forge_request_outcome CHECK (outcome IN ('', 'merged', 'conflict', 'behind', 'stale_head', 'refused'))
);

INSERT INTO forge_requests_next
  (workspace_id, id, kind, work_order_id, repository_id, worker_id, binding, branch, base, head_commit, number, title, body,
   attempt, state, detail, requested_at, reported_at)
SELECT
   workspace_id, id, kind, work_order_id, repository_id, worker_id, binding, branch, base, head_commit, number, title, body,
   attempt, state, detail, requested_at, reported_at
FROM forge_requests;

DROP TABLE forge_requests;

ALTER TABLE forge_requests_next RENAME TO forge_requests;

CREATE INDEX forge_requests_worker ON forge_requests (workspace_id, worker_id, state);
CREATE INDEX forge_requests_work_order ON forge_requests (workspace_id, work_order_id);

-- Every approval of a reviewed commit for merge, and the merge that followed it (FR-MERGE-6): who approved,
-- at which level, and what landed. A review approves only at review_approves, and a merge made has a time.
CREATE TABLE integration_records (
  workspace_id     TEXT   NOT NULL,
  id               TEXT   NOT NULL,
  work_order_id    TEXT   NOT NULL,
  pull_request     BIGINT NOT NULL DEFAULT 0,
  approved_commit  TEXT   NOT NULL,
  merge_authority  TEXT   NOT NULL,
  approved_by_kind TEXT   NOT NULL,
  approved_by_id   TEXT   NOT NULL,
  approved_at      BIGINT NOT NULL,
  merged_commit    TEXT   NOT NULL DEFAULT '',
  merged_at        BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT integration_approver CHECK (approved_by_kind IN ('member', 'review', 'forge')),
  CONSTRAINT integration_level CHECK (merge_authority IN ('person_merges', 'person_approves', 'review_approves')),
  CONSTRAINT integration_review_level CHECK (approved_by_kind <> 'review' OR merge_authority = 'review_approves'),
  CONSTRAINT integration_commit CHECK (approved_commit <> ''),
  CONSTRAINT integration_merged CHECK (merged_commit = '' OR merged_at > 0)
);

CREATE INDEX integration_records_work_order ON integration_records (workspace_id, work_order_id);

-- A merge approval request belongs to a work order and no lane: a member approves the merge of a reviewed
-- commit at person_approves, or decides a merge the forge or git refused. Only a member decides one.
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
  CONSTRAINT approval_names_its_lane CHECK (kind IN ('plan', 'recovery', 'merge') OR (lane_id IS NOT NULL AND work_order_id IS NOT NULL AND generation IS NOT NULL)),
  CONSTRAINT approval_plan_is_a_members CHECK (kind <> 'plan' OR decided_by IS NULL OR decided_kind = 'member'),
  CONSTRAINT approval_plan_only CHECK (kind = 'plan' OR (plan_id IS NULL AND plan_version IS NULL)),
  CONSTRAINT approval_recovery_names_its_work CHECK (kind <> 'recovery' OR work_order_id IS NOT NULL),
  CONSTRAINT approval_recovery_is_a_members CHECK (kind <> 'recovery' OR decided_by IS NULL OR decided_kind = 'member'),
  CONSTRAINT approval_merge_names_its_work CHECK (kind <> 'merge' OR (work_order_id IS NOT NULL AND lane_id IS NULL)),
  CONSTRAINT approval_merge_is_a_members CHECK (kind <> 'merge' OR decided_by IS NULL OR decided_kind = 'member')
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
