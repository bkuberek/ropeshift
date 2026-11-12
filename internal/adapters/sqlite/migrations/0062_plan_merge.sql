-- Merging a planning branch (ADR-0018, ADR-0020). A member's plan approval is the approval of the planning
-- branch's commit for merge, and the record names the plan version it approved. Work that is not planning
-- names none.
ALTER TABLE integration_records ADD COLUMN plan_id TEXT NOT NULL DEFAULT '';
ALTER TABLE integration_records ADD COLUMN plan_version BIGINT NOT NULL DEFAULT 0;
