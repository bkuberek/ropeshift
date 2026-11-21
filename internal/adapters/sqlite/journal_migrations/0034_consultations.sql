-- The consultations this worker runs for its orchestrator, kept from before each one's session starts until the event
-- that carries its end is stored (FR-ORCH-9). `run` is what the worker needs to find the session a restart left and to
-- record its end: the ledger's grant, and the project and orchestrator lane it was granted to. A row still here when a
-- worker starts is a consultation whose end the worker never journaled.
CREATE TABLE consultations (
  id  TEXT PRIMARY KEY,
  run TEXT NOT NULL
);
