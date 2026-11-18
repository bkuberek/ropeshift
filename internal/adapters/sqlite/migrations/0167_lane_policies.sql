-- The policy versions a lane started under, as JSON (ADR-0037): every lane records the versions in force when it
-- started, so a member can read what it ran on after a version changes. The empty text is a lane that started
-- before they were kept.
ALTER TABLE lanes ADD COLUMN policies TEXT NOT NULL DEFAULT '';
