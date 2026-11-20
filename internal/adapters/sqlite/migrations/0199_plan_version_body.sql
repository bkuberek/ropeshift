-- The text of the plan's file a plan version was read from, which a member reads when asked to approve the
-- version: the Markdown after its front matter, cut at a bound, and whether it was cut.
ALTER TABLE plan_versions ADD COLUMN body TEXT NOT NULL DEFAULT '';
ALTER TABLE plan_versions ADD COLUMN body_cut BIGINT NOT NULL DEFAULT 0;
