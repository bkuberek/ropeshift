-- What a member decided after a review had judged the work (FR-CAL-1). A record is bound to the review and
-- to the commit digest it read, and it holds the merge authority in force when the member decided and the
-- verdicts the member saw, so a record reads on its own at the level it was made at. Nothing in it, and
-- nothing computed from it, ever changes merge_authority.
--
-- agreement is agree, disagree, or unknown, the last for a review that could not judge. source is the
-- acceptance step (decision) or a defect found in work that was accepted after a pass (later_defect). A
-- review is decided once per source and decision, so a repeated write changes nothing.

CREATE TABLE review_calibrations (
  workspace_id    TEXT   NOT NULL,
  id              TEXT   NOT NULL,
  work_order_id   TEXT   NOT NULL,
  review_id       TEXT   NOT NULL,
  commit_digest   TEXT   NOT NULL,
  repository_id   TEXT   NOT NULL DEFAULT '',
  merge_authority TEXT   NOT NULL,
  outcome         TEXT   NOT NULL,
  verdicts        TEXT   NOT NULL,
  decision        TEXT   NOT NULL,
  agreement       TEXT   NOT NULL,
  source          TEXT   NOT NULL,
  member_id       TEXT   NOT NULL DEFAULT '',
  reason          TEXT   NOT NULL DEFAULT '',
  created_at      BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id),
  CONSTRAINT review_calibration_outcome CHECK (outcome IN ('pass', 'fail', 'cannot_judge')),
  CONSTRAINT review_calibration_decision CHECK (decision IN ('accept', 'rework')),
  CONSTRAINT review_calibration_agreement CHECK (agreement IN ('agree', 'disagree', 'unknown')),
  CONSTRAINT review_calibration_source CHECK (source IN ('decision', 'later_defect'))
);

CREATE UNIQUE INDEX review_calibrations_once ON review_calibrations (workspace_id, review_id, source, decision);

CREATE INDEX review_calibrations_work ON review_calibrations (workspace_id, work_order_id);
