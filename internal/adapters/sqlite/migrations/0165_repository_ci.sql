-- A repository's `[ci]` keys (ADR-0037), as JSON: how the control plane reads CI for it. They sit on the record with
-- its merge method and review target. A record nobody edited holds the empty text and reads as the defaults, so
-- every record the ledger holds now keeps the defaults until a member changes a key.
ALTER TABLE repositories ADD COLUMN ci TEXT NOT NULL DEFAULT '';
