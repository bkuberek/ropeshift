-- What a SessionStart hook said a pane's session runs on. A session's socket changes with
-- its process, and a worker that was down reads it here.

ALTER TABLE hook_events ADD COLUMN socket TEXT NOT NULL DEFAULT '';
ALTER TABLE hook_events ADD COLUMN model TEXT NOT NULL DEFAULT '';
