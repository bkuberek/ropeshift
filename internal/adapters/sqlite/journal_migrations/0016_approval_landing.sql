-- A decision binds to where a call lands, which a hook cannot read, so a request also keeps the
-- digest a hook names its call by.

ALTER TABLE open_approvals ADD COLUMN seen TEXT NOT NULL DEFAULT '';
