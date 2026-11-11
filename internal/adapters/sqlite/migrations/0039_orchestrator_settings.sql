-- The settings an orchestrator's current run bound with. An orchestrator serves every project of an
-- installation, so they stay with its record, where a decision of any project reads them.
ALTER TABLE orchestrators ADD COLUMN settings TEXT NOT NULL DEFAULT '';
