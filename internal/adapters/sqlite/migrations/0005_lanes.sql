-- The lane a member asked to run under a claim, which is its desired state.

ALTER TABLE claims ADD COLUMN lane_id TEXT;
