-- A question keeps the shape its asker gave it: a title of a few words, what each option trades off, as JSON keyed by
-- the option, and what the answer unblocks. A question asked before these columns holds none, which reads as empty.

ALTER TABLE questions ADD COLUMN title TEXT;
ALTER TABLE questions ADD COLUMN tradeoffs TEXT;
ALTER TABLE questions ADD COLUMN unblocks TEXT;
