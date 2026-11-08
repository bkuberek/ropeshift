-- What a worker last said of its orchestrator beside its state, and when. An orchestrator has no lane
-- row, so this is where a person reads that one stalled.
ALTER TABLE orchestrators ADD COLUMN conditions TEXT NOT NULL DEFAULT '';
ALTER TABLE orchestrators ADD COLUMN reported_at BIGINT NOT NULL DEFAULT 0;
