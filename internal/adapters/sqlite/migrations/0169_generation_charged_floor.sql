-- The most a generation has been charged (ADR-0037): the highest charge of any usage its worker reported. A turn of unknown
-- cost is charged at the mean of the known ones, so a known turn that costs little lowers the mean and the charge with it,
-- though every figure the generation recorded only grew. The floor holds the charge where it was, so a hard limit that
-- stopped a work order is not lifted by a cheap turn that lands after the stop. A generation that never reported holds zero.
ALTER TABLE generations ADD COLUMN charged_floor_usd DOUBLE PRECISION NOT NULL DEFAULT 0;
