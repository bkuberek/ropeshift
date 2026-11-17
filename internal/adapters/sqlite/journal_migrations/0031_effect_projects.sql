-- The project of the lane an effect belongs to (ADR-0021), kept with the effect so what the ledger is told
-- of it reaches that project even when the lane has no record. An empty project is the plane's only one,
-- which an effect recorded before this holds.
ALTER TABLE effects ADD COLUMN project TEXT NOT NULL DEFAULT '';
