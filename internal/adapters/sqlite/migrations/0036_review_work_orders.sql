-- A `review` work order names the work order it judges and the attempt of it: the fence that work order's
-- lane ended at, whose branch artifact holds the commit under review. Empty on every other work order.

ALTER TABLE work_orders ADD COLUMN reviews TEXT;
ALTER TABLE work_orders ADD COLUMN reviews_fence BIGINT;
