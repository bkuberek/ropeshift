-- Why an orchestrator's current run ended: the reason its worker gave, and the cause beside it as plain text with no
-- credential, which Needs you shows while a member still wants it to run. Ledger migration 0202 gives a store with no
-- installation the same columns. A run that ended before these columns reads as ended with no reason.
ALTER TABLE orchestrators ADD COLUMN end_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE orchestrators ADD COLUMN end_detail TEXT NOT NULL DEFAULT '';
