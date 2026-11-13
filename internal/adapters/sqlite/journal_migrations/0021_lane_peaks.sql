-- The peaks of the last lanes this worker ran, which its lane cap recommendation reads once enough have
-- run (FR-OBS-9). They outlive the process, so a restart does not start the count again.

CREATE TABLE lane_peaks (
  id    INTEGER PRIMARY KEY CHECK (id = 1),
  peaks TEXT NOT NULL
);
