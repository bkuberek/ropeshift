-- How far a lane's parking got, so a restart finishes it. A stored wake decides what a
-- restarted worker does.

ALTER TABLE lanes ADD COLUMN parking BLOB;
ALTER TABLE lanes ADD COLUMN blocking BLOB;
