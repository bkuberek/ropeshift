-- A lane's own profile layer, which travels with the request to run it.

ALTER TABLE claims ADD COLUMN lane_overrides TEXT NOT NULL DEFAULT '';
