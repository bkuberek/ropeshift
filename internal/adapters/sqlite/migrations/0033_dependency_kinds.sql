-- A dependency is known by its kind as well as by the work order and its target. Intake derives two
-- more kinds from a plan's structure, one from the milestones a work order's milestone depends on and
-- one from the shared contracts its contract uses, and a derived dependency can name the same target
-- as one a file writes. The table is made again with the kind in its key, because a key cannot be widened
-- in place in every database.

CREATE TABLE dependencies_next (
  workspace_id  TEXT NOT NULL,
  work_order_id TEXT NOT NULL,
  depends_on    TEXT NOT NULL,
  adapter       TEXT,
  instance      TEXT,
  kind          TEXT NOT NULL,
  PRIMARY KEY (workspace_id, work_order_id, depends_on, kind)
);

INSERT INTO dependencies_next (workspace_id, work_order_id, depends_on, adapter, instance, kind)
SELECT workspace_id, work_order_id, depends_on, adapter, instance, kind FROM dependencies;

DROP TABLE dependencies;

ALTER TABLE dependencies_next RENAME TO dependencies;
