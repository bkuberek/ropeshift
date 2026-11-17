# The Postgres adapter

`internal/adapters/postgres` keeps an installation on one Postgres database: the installation's own records in the `installation` schema, and each project in a schema of its own, named `project_<id>` with the ID in lowercase. It shares the statements of `internal/adapters/sqlstore` with the SQLite adapter, and `make check-postgres` runs the store contract suite and the adapter's own tests on a private server. The server's binaries come from `PATH` or from the directory `ROPESHIFT_TEST_PG_BIN` names. No solo binary imports the adapter, and `make check` holds that.

## Connections

The projects of an installation share one pool of connections, and the installation has a pool of its own. `Options.MaxConns` bounds each of them, and neither is ever smaller than 2. The two are separate because one pool deadlocked: a project's transaction holds a connection while it reads the installation's records through a second one, so enough writers on one pool each held a connection and none got the second.

A project's write holds one connection of its pool, and at most one of the installation's. The installation's side of a write begins at its first statement, read-only, and a write that registers a repository swaps it for a writing one at that point. The side commits, or ends if it only read, when the write's function returns and before the project's transaction commits.

## The migration lock

Every step that makes a schema, migrates one, or drops one runs under a session advisory lock on one fixed key (`postgres.MigrationLock`), so processes that start together apply each version once. A caller takes its turn in its own process first, and then polls `pg_try_advisory_lock` on a connection of the project pool, which it keeps for as long as it holds the lock. The work under the lock runs on other connections of the pool, so the pool is never smaller than 2.

A caller waits for the lock until its context ends or `Options.LockTimeout` passes, one minute unless the option says otherwise, and then fails with `postgres.ErrLockTimeout`. A project whose Store the process already holds comes back from `Open` without touching the lock. A session that holds the lock and stops is a defect to find on the server: `pg_locks` lists it with `locktype = 'advisory'`.

## Creating, opening, and removing a project

The registry decides which projects exist. A project's schema is named `project_` and its ID in lowercase, so a generated ID, which is in capitals, gives a name `psql` reads without quotes. Two IDs that differ only in case name one schema, and `State.Create` refuses the second. A project an earlier build registered on a schema that kept the ID's capitals still opens and is removed with that schema.

`State.Create` runs under the migration lock from the first check to the registry's row: it refuses a name or a schema another project holds, makes the schema, migrates it, makes the workspace, and registers the project. A crash after the schema and before the registry's row leaves a schema no project names. A retry with the same ID registers it, and `postgres.Open` drops the rest: it drops every schema that starts with `project_` and that no registered project names, under the lock, so one that a `Create` is making is never one it drops. The prefix is ropeshift's in its database, and a schema of that form that a person made goes too.

`State.Open` returns the Store the process already keeps for a project, and otherwise reads the registry under the lock and refuses a project it does not hold with `ports.ErrNotFound`, so it never makes the schema of a project a removal has forgotten. `State.Remove` deletes the registry's row and then drops the schema under the same lock, so an `Open` or a `Create` in another process cannot make the schema again while it drops.

## Idle transactions

Every connection sets `idle_in_transaction_session_timeout`, 30 seconds unless `Options.IdleInTransaction` says otherwise, so a process stopped inside a transaction cannot stall every writer of its project. The two transactions of a write are idle in turn: the project's while the installation's side runs a statement, and the side's while the project runs one.

A project statement that runs longer than the timeout between two uses of the installation's side ends the side's session, and the write fails. The same holds the other way: a statement of the side that waits for a lock, such as the repository index's, can outlast the timeout of the project's session. Writes are short by design, because the project's revision lock is taken first (ADR-0010). A deployment that runs a long statement in a write sets `IdleInTransaction` above the longest one, or turns the timeout off with a negative value.
