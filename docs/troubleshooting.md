# Troubleshooting

This page covers problems that stop ropeshift, what each one means, and what to do about it.

## Database refuses to start

Every database in the state directory records a checksum of each schema change it applied: the installation database, each project's ledger, the telemetry store, and the worker's journal. Before the daemon applies anything to a database, it compares those checksums with the schema changes its build ships. When one differs, the daemon changes nothing in that database, prints an error like this one, and exits:

```text
ropeshiftd solo: upgrade <state directory>: migrated: installation migration 1 (0001_installation.sql) changed after it was applied: recorded sha256 <recorded>, shipped sha256 <shipped>
to start, run the build that migrated this database, or restore the state directory from a backup; docs/troubleshooting.md#database-refuses-to-start says how: <link to this page>
```

A release never changes a schema change it shipped, so another build migrated this database, usually a development build. Do one of these:

- Run the build that migrated the database.
- Move the current state directory aside, so nothing the other build wrote is lost, and restore the state directory from a copy made before that build ran.

Backups made with `ropeshift backup` cover the installation database and each project's ledger, as [backing up](onboarding.md#back-up-a-project-and-the-installation) describes. They do not cover the telemetry store or the worker's journal. When the refusal names one of those two, run the build that migrated it, or restore a full copy of the state directory.

A database that a later release migrated does not stop an earlier one. After a downgrade, the daemon prints a warning naming each database whose applied schema changes its build does not ship, and the schema changes themselves, and starts on the schema the later release left. A project's ledger can warn more than once in one start.
