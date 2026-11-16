// Package postgres is the group-mode Store adapter: the statements of sqlstore on a Postgres database,
// where the installation and each project are schemas of one database (ADR-0010, ADR-0021). The projects
// share one connection pool, and the installation has a pool of its own, because a project's transaction
// reads the shared records through the installation's. It compiles in every build, and no solo-mode
// wiring imports it.
package postgres
