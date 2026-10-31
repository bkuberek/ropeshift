-- Where a lane's state stood, so an adopted run goes on from there.

ALTER TABLE lanes ADD COLUMN actual TEXT NOT NULL DEFAULT 'pending';
