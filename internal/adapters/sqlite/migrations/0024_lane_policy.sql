-- What a policy asked of a running lane. Empty means nothing is asked, and a parked commit clears
-- it, so a lane that comes back does not park again on the same word.

ALTER TABLE lanes ADD COLUMN park_wanted TEXT NOT NULL DEFAULT '';
