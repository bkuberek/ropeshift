-- The mode a lane's session started in, because a resume stays in it.

ALTER TABLE lanes ADD COLUMN run_mode TEXT NOT NULL DEFAULT '';
