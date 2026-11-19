-- A member's acknowledgement of a practices finding (FR-PRAC-10): the reason a repository leaves a finding open, and
-- who said so and when. The finding stays in every view of the result, marked with these. An acknowledgement is keyed
-- by the repository and the finding's ID, which stays the same across checks, so it keeps holding for the finding
-- until a member withdraws it.

CREATE TABLE practices_acknowledgements (
  workspace_id    TEXT   NOT NULL,
  repository_id   TEXT   NOT NULL,
  finding_id      TEXT   NOT NULL,
  reason          TEXT   NOT NULL,
  member          TEXT   NOT NULL,
  acknowledged_at BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, repository_id, finding_id)
);
