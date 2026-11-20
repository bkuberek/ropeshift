-- The agent CLI version a lane's worker is probing, as the worker last said, when the lane's process runs that
-- version: its driver and its version. The empty text is a lane no probe holds.
ALTER TABLE lanes ADD COLUMN checking_driver TEXT NOT NULL DEFAULT '';
ALTER TABLE lanes ADD COLUMN checking_version TEXT NOT NULL DEFAULT '';
