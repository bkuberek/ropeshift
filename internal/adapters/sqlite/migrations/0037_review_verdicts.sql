-- How much of each change the person decides after the review: person_merges, person_approves, or
-- review_approves. The code holds the three names, and a record that predates the column reads as the
-- default, which leaves every change to the person.

ALTER TABLE repositories ADD COLUMN merge_authority TEXT NOT NULL DEFAULT 'person_merges';

-- What a review lane said of each criterion of the work it judged. A verdict is bound to the commit the
-- review read, and to the attempt that left it, so a branch that moved or a later attempt is never judged
-- by an earlier verdict. The lane, its generation, and the fence it ended at say who gave it, and
-- max_rounds is the bound on review rounds the review lane's profile named, which the work is settled
-- against whenever its verdicts are read again. A review completes once, so a criterion has one verdict
-- per review.

CREATE TABLE review_verdicts (
  workspace_id  TEXT   NOT NULL,
  review_id     TEXT   NOT NULL,
  work_order_id TEXT   NOT NULL,
  attempt       BIGINT NOT NULL,
  commit_digest TEXT   NOT NULL,
  lane_id       TEXT   NOT NULL,
  generation    BIGINT NOT NULL,
  fence         BIGINT NOT NULL,
  criterion_id  TEXT   NOT NULL,
  verdict       TEXT   NOT NULL,
  reasons       TEXT   NOT NULL,
  citations     TEXT   NOT NULL,
  max_rounds    BIGINT NOT NULL DEFAULT 0,
  created_at    BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, review_id, criterion_id),
  CONSTRAINT review_verdict_kind CHECK (verdict IN ('pass', 'fail', 'cannot_judge'))
);

CREATE INDEX review_verdicts_work ON review_verdicts (workspace_id, work_order_id, attempt);

-- The reviews of a work order are read whenever its standing is.

CREATE INDEX work_orders_reviews ON work_orders (workspace_id, reviews);

-- Why completed work went back for another attempt. A rework stays on the same work order, so the next
-- lane's brief reads the newest note: the reason a member gave, or the review whose failed criteria
-- sent it back. The attempt is the fence the work came back from.

CREATE TABLE rework_notes (
  workspace_id  TEXT   NOT NULL,
  work_order_id TEXT   NOT NULL,
  attempt       BIGINT NOT NULL,
  review_id     TEXT   NOT NULL DEFAULT '',
  reason        TEXT   NOT NULL,
  created_at    BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, work_order_id, attempt)
);
