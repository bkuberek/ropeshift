-- What this worker's last probe observed of its CLI's version, which outlives its process, so a
-- restart on the same version does not probe again. It holds versions, surfaces, and results.

CREATE TABLE qualification (
  id    INTEGER PRIMARY KEY CHECK (id = 1),
  state TEXT NOT NULL
);
