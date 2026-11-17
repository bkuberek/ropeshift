-- The server a worker joined, which its link dials: the one address a group worker reaches its control plane
-- at. A solo worker joins no server and holds none.

CREATE TABLE server_link (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  server_url TEXT NOT NULL
);
