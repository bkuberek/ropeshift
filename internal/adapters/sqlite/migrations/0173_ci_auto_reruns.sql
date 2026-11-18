-- The automatic reruns of one head's CI (ADR-0037), counted apart from a member's own. `ci_auto_rerun` bounds the
-- automatic ones, and each member's explicit rerun comes on top of them, so the cap reads this column and `reruns`
-- keeps counting every rerun of the head. A head with no row, or a row from before this column, has none.
ALTER TABLE ci_waits ADD COLUMN auto_reruns BIGINT NOT NULL DEFAULT 0 CHECK (auto_reruns >= 0);
