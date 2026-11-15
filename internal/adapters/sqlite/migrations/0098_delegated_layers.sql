-- The delegate that sent a claim's lane its own profile layer, as its actor ID names it, the version of the
-- delegate policy in force when it did, and whether that version keeps the layer from raising the lane's model
-- or effort (FR-AUTH-16). All are empty when a member, an orchestrator, or the system started the lane.
ALTER TABLE claims ADD COLUMN overrides_delegate TEXT NOT NULL DEFAULT '';
ALTER TABLE claims ADD COLUMN overrides_policy_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE claims ADD COLUMN overrides_restricted BIGINT NOT NULL DEFAULT 0;
