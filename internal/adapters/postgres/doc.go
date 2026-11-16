// Package postgres is the group-mode Store adapter: the statements of sqlstore on a Postgres database,
// where the installation and each project are schemas of one database that share one connection pool
// (ADR-0010, ADR-0021). It compiles in every build, and no solo-mode wiring imports it.
package postgres
