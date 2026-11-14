-- Who prepared the call an event's actor ran: the delegate whose confirmation card a member confirmed
-- (ADR-0024). Both are empty when the actor's call was its own.
ALTER TABLE events ADD COLUMN proposer_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN proposer_id TEXT NOT NULL DEFAULT '';
