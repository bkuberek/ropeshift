-- The project each lane, event, and lease belongs to (ADR-0021), beside the lane, because one worker serves
-- every project of an installation. An empty project is the plane's only one, which a journal written
-- before projects holds throughout. Two projects can hold a work order with the same ID, so a lease is
-- known by both.
ALTER TABLE lanes ADD COLUMN project TEXT NOT NULL DEFAULT '';

ALTER TABLE events ADD COLUMN project TEXT NOT NULL DEFAULT '';

CREATE TABLE project_leases (
  project    TEXT   NOT NULL DEFAULT '',
  work_order TEXT   NOT NULL,
  claim      TEXT   NOT NULL,
  fence      BIGINT NOT NULL,
  PRIMARY KEY (project, work_order)
);

INSERT INTO project_leases (project, work_order, claim, fence) SELECT '', work_order, claim, fence FROM leases;

DROP TABLE leases;

ALTER TABLE project_leases RENAME TO leases;
