-- The restructuring a member chose when they adopted a project (FR-ADOPT-12), as JSON: the shape, the digest of
-- the plan it was chosen from, and for a consolidation the design repository and the code trees to leave out.
-- Only an adopt work order holds one, and every other work order's is NULL.
ALTER TABLE work_orders ADD COLUMN restructure TEXT;
