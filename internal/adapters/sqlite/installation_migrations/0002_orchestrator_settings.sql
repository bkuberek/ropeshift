-- The settings an orchestrator's current run bound with, as a ledger's orchestrators table holds them.
ALTER TABLE orchestrators ADD COLUMN settings TEXT NOT NULL DEFAULT '';
