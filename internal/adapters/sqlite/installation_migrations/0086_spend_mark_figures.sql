-- What an owner's generation was charged in a scope of spend is kept as ReportUsage keeps a generation's usage: the highest
-- of each figure its worker reported, and the highest charge any one report reached, which is the floor. Charged spend is
-- what those figures charge together, so a report that carries a lower cost and more turns than another no longer hides
-- the figures of the other. A mark an earlier build wrote held only its highest charge, which is kept as its floor, so
-- no generation reads lower after the upgrade.
ALTER TABLE spend_marks ADD COLUMN cost_usd      DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE spend_marks ADD COLUMN known_turns   BIGINT           NOT NULL DEFAULT 0;
ALTER TABLE spend_marks ADD COLUMN unknown_turns BIGINT           NOT NULL DEFAULT 0;
ALTER TABLE spend_marks ADD COLUMN floor_usd     DOUBLE PRECISION NOT NULL DEFAULT 0;
UPDATE spend_marks SET floor_usd = charged_usd;
