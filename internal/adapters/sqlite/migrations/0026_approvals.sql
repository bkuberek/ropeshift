-- Requests bound to an exact artifact. The checks are the rule itself, so that no code path can get
-- past them: nobody decides their own request, only a member or an orchestrator decides, a decision
-- names the digest it saw and that digest is the request's, and nothing is approved with nobody
-- behind it. A digest never changes on a row: a changed artifact is a new request.

CREATE TABLE approval_requests (
  workspace_id   TEXT   NOT NULL,
  id             TEXT   NOT NULL,
  lane_id        TEXT   NOT NULL,
  work_order_id  TEXT   NOT NULL,
  generation     BIGINT NOT NULL,
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
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT approval_no_self_decision CHECK (decided_by IS NULL OR decided_by <> requested_by),
  CONSTRAINT approval_decider_kind CHECK (decided_by IS NULL OR decided_kind IN ('member', 'orchestrator')),
  CONSTRAINT approval_decider_name CHECK (decided_by IS NULL OR decided_by LIKE decided_kind || ':%'),
  CONSTRAINT approval_decided_digest CHECK (decided_by IS NULL OR decided_digest = subject_digest),
  CONSTRAINT approval_has_a_decider CHECK (state NOT IN ('approved', 'used') OR decided_by IS NOT NULL)
);

CREATE INDEX approval_requests_lane ON approval_requests (workspace_id, lane_id);
CREATE INDEX approval_requests_work_order ON approval_requests (workspace_id, work_order_id);
CREATE INDEX approval_requests_state ON approval_requests (workspace_id, state);
