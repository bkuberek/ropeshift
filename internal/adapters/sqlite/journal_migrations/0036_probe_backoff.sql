-- The wait before the next probe of the version the last probe checked, and when it ends, kept with the attempt, so a
-- worker that restarts probes a version that keeps failing when its wait ends and not when the restart delay does. Both are
-- zero where no wait stands, as they are for an attempt an earlier build kept. They hold a duration and a time, and nothing
-- of a session or an account.

ALTER TABLE probe_attempt ADD COLUMN wait_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE probe_attempt ADD COLUMN next_ms INTEGER NOT NULL DEFAULT 0;
