-- How a lane's last stop left its worktree, and what each lane holds beside it. Allocations
-- live here so a restart can give back what a crash left. A block of zero ports shares the
-- base, so only a real block is unique.

ALTER TABLE lanes ADD COLUMN guard BLOB;

CREATE TABLE lane_allocations (
  lane            TEXT PRIMARY KEY,
  port_base       BIGINT NOT NULL,
  ports           BIGINT NOT NULL,
  compose_project TEXT   NOT NULL
);

CREATE UNIQUE INDEX lane_allocations_block ON lane_allocations (port_base) WHERE ports > 0;
