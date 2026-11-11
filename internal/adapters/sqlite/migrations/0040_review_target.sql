-- Where a repository's accepted work is reviewed and lands (ADR-0020): on a branch, the default, or in a
-- pull request through the repository's forge. It replaces the switch that said whether ropeshift
-- integrates accepted work: off reviewed on a branch, and on opened a pull request.

ALTER TABLE repositories ADD COLUMN review_target TEXT NOT NULL DEFAULT 'branch'
  CONSTRAINT repository_review_target CHECK (review_target IN ('branch', 'pull_request'));

UPDATE repositories SET review_target = 'pull_request' WHERE integration <> 0;

ALTER TABLE repositories DROP COLUMN integration;
