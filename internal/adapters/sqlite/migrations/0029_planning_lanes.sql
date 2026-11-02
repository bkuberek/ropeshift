-- What a planning lane and a review lane add to a work order. A `contract_review` work order names the
-- plan it reads and the contract versions it read them at, and a `plan` work order names the commit
-- intake took its files from once it was accepted (D17).

ALTER TABLE work_orders ADD COLUMN reviews_plan TEXT;
ALTER TABLE work_orders ADD COLUMN review_pins TEXT;
ALTER TABLE work_orders ADD COLUMN plan_commit TEXT;
