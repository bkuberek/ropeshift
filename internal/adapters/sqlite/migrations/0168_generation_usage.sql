-- What each generation spent (ADR-0037), as its worker last reported it: the cost its turns reported, how many turns
-- reported one, and how many could not, because a stopped turn reports nothing. Charged spend folds these across
-- every lane of a work order, charging each turn of unknown cost at the mean of the known ones. A generation that
-- never reported holds zero.
ALTER TABLE generations ADD COLUMN cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE generations ADD COLUMN known_turns BIGINT NOT NULL DEFAULT 0;
ALTER TABLE generations ADD COLUMN unknown_turns BIGINT NOT NULL DEFAULT 0;
