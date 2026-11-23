-- Why an orchestrator's current run ended: the reason its worker gave, and the cause beside it as plain text with no
-- credential, which Needs you shows while a member still wants it to run. The installation keeps them in installation
-- migration 0089, and these are the columns a store with no installation reads and writes with the same statements. A
-- run that ended before these columns reads as ended with no reason.
ALTER TABLE orchestrators ADD COLUMN end_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE orchestrators ADD COLUMN end_detail TEXT NOT NULL DEFAULT '';
