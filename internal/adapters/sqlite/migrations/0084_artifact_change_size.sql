-- What a lane's branch changed between its base commit and its final commit, as its worker measured it
-- (FR-OBS-13): the files it changed and the lines it added and removed. All three are null on an artifact
-- nothing was measured for, which is not a change of nothing.
ALTER TABLE artifacts ADD COLUMN files_changed BIGINT;
ALTER TABLE artifacts ADD COLUMN lines_added BIGINT;
ALTER TABLE artifacts ADD COLUMN lines_removed BIGINT;
