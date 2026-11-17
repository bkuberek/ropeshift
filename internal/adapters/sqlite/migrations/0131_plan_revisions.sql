-- A plan needs revision once a member decides something inside the plan's approved version (ADR-0030). One
-- row holds what a member is asked to settle, from the first such decision to the Confirm or the approval that
-- ends it. decision_ids lists the decisions since the approved version in the order they were recorded, and
-- finished lists the finished work orders the reversible ones reached, each with the decisions that reached it
-- and the rework a member made of it. A plan has at most one open revision. outcome is `confirmed` when a
-- member confirmed the approved version, and `approved` when a member approved a later version, whose request
-- approval_id names.
CREATE TABLE plan_revisions (
  workspace_id     TEXT   NOT NULL,
  id               TEXT   NOT NULL,
  plan_id          TEXT   NOT NULL,
  approved_version BIGINT NOT NULL,
  decision_ids     TEXT   NOT NULL,
  finished         TEXT   NOT NULL,
  opened_at        BIGINT NOT NULL,
  closed_at        BIGINT,
  closed_by        TEXT   NOT NULL DEFAULT '',
  outcome          TEXT   NOT NULL DEFAULT '',
  approval_id      TEXT   NOT NULL DEFAULT '',
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT plan_revision_outcome CHECK (outcome IN ('', 'confirmed', 'approved'))
);

CREATE UNIQUE INDEX plan_revisions_open ON plan_revisions (workspace_id, plan_id) WHERE closed_at IS NULL;
CREATE INDEX plan_revisions_plan ON plan_revisions (workspace_id, plan_id, opened_at);
