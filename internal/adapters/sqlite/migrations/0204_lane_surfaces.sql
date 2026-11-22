-- Where the surfaces a lane's process started on come from, as the lane's worker last said: the shipped matrix when
-- its text is `matrix`, and empty otherwise, for surfaces a probe qualified and for documented surfaces alike. It stays
-- after the lane's check ends.
ALTER TABLE lanes ADD COLUMN lane_surfaces TEXT NOT NULL DEFAULT '';
