-- A work order that finishes an in-flight branch (ADR-0023). The work order names the branch, and its claim
-- records the ref and the tip its lane's branch began at. The claim's base commit is then where the branch
-- left the work order's base, which review diffs from, and the branch itself never moves.
ALTER TABLE work_orders ADD COLUMN continues TEXT;

ALTER TABLE claims ADD COLUMN continues_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE claims ADD COLUMN continues_commit TEXT NOT NULL DEFAULT '';
