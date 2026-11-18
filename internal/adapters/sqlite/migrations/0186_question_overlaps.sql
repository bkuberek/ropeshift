-- An escalated question keeps the decisions in force whose scope overlapped its own, as a JSON array of decision
-- IDs, so the item a member answers can name each one an answer may replace (ADR-0030). A question escalated before
-- this column holds none, which reads as empty.

ALTER TABLE questions ADD COLUMN overlapping TEXT;
