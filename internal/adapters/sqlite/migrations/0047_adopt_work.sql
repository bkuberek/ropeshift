-- A planning work order that adopts an existing project (ADR-0023). Its lane is a planning lane told to
-- onboard the project, and the worker writes the inventory of the project's repositories before it starts.
ALTER TABLE work_orders ADD COLUMN adopt BIGINT NOT NULL DEFAULT 0;
