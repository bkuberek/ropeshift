-- What the repository file format says of work that already exists (ADR-0013, ADR-0023): a requirement the
-- code meets, with the evidence that shows it; a contract the code delivers; and what a plan for an existing
-- project left out, with each reason. An empty status is open.
ALTER TABLE requirements ADD COLUMN status TEXT NOT NULL DEFAULT '';
ALTER TABLE requirements ADD COLUMN evidence TEXT NOT NULL DEFAULT '[]';

ALTER TABLE contracts ADD COLUMN existing BIGINT NOT NULL DEFAULT 0;

ALTER TABLE plan_versions ADD COLUMN left_out TEXT NOT NULL DEFAULT '[]';
