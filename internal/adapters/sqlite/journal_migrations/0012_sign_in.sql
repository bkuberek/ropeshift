-- What this worker knows of its one sign-in, which outlives its process. It may hold free text that
-- names local plugins, so nothing here leaves the machine.

CREATE TABLE sign_in (
  id    INTEGER PRIMARY KEY CHECK (id = 1),
  state TEXT NOT NULL
);
