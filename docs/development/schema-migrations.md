# Schema migrations

This page describes how each ropeshift database reaches the schema of the build that opens it, and the checksum rule that stops a start when a migration it applied before has changed.

## The migration sets

Every database holds the migrations it applied in a table of its own, and a start applies, in order, each shipped migration that table does not list yet. Migrations run forward only: there is no step that undoes one.

| Set | Database | Files | Applied migrations table | Runner |
|-----|----------|-------|--------------------------|--------|
| `ledger` | Each project's ledger | `internal/adapters/sqlite/migrations/`, and one database's own files: `dialect_migrations/` for SQLite, `test/pgtest/migrations/` for Postgres | `schema_migrations`, by version | `sqlstore.Store.Migrate` |
| `installation` | The installation database | `internal/adapters/sqlite/installation_migrations/` | `schema_migrations`, by version | `sqlstore.Installation.Migrate` |
| `journal` | The worker's journal | `internal/adapters/sqlite/journal_migrations/` | `journal_migrations`, by file name | `sqlite.Journal.Migrate` |
| `telemetry` | The worker's telemetry store | `internal/adapters/sqlite/telemetry_migrations/` | `telemetry_migrations`, by file name | `sqlite.TelemetryStore.Migrate` |

A migration's file name starts with its version, as in `0035_repositories.sql`. Versions in a set need not be consecutive, and a version one database's own files take is left free in the neutral files. Each migration runs in its own transaction with its record, so a crash leaves it applied and recorded, or neither.

## Checksums

Each applied migration's record holds the SHA-256, in lowercase hex, of the migration's text exactly as the build embeds it. On every start, before it applies anything, the runner compares each recorded migration with the one the build ships:

- A record with no checksum, which a build from before checksums made, takes the checksum of the shipped file under its version, or its name for the journal and the telemetry store. The first start after the upgrade records it.
- A recorded checksum that differs from the shipped file's refuses the start, with an error that names the set, the version, the file, and both checksums.
- A recorded migration the build no longer ships refuses the start, with an error that names the set, the version, the file, and the recorded checksum.

A refusal applies no migration and records no checksum, and it lists every migration that differs. The daemon migrates the installation database, every project's ledger, the journal, and the telemetry store as it starts, and a refusal from any of them stops the start: the daemon prints it and exits, so no database runs with a schema the build's statements do not expect. The errors wrap `sqlstore.ErrMigrationChanged` and `sqlstore.ErrMigrationNotShipped`, and `sqlstore.MigrationError` carries each field.

## Changing the schema

Because of the checksum rule, a migration file that a released build shipped is never edited, renamed to another version, or deleted:

- Change the schema with a new file under the next free version of its set.
- Put a change only one database can express in that database's own files, under a version the neutral files leave free.
- Fix a mistake in a shipped migration with a new migration that corrects it.

While a migration is unreleased, editing it is safe for users, but a database a maintainer migrated with the earlier text refuses to start. Start that database again from empty.

`MergeMigrations` refuses two files of one set that share a version, and the tests in `test/contract/migration_checks.go` and `internal/adapters/sqlite/file_migrations_test.go` hold the checksum rule. `make check-postgres` runs the first on a private Postgres too.
