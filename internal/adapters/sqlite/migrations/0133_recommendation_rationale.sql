-- An escalated question keeps why its recommendation is the one to choose (ADR-0030). Accepting the
-- recommendation records that rationale as the decision's. A question escalated before this column holds
-- none, which reads as empty.

ALTER TABLE questions ADD COLUMN recommendation_rationale TEXT;
