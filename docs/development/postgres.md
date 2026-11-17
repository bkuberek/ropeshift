# The Postgres adapter

`internal/adapters/postgres` keeps an installation on one Postgres database: the installation's own records in the `installation` schema, and each project in a schema of its own, named `project_<id>` with the ID in lowercase. It shares the statements of `internal/adapters/sqlstore` with the SQLite adapter, and `make check-postgres` runs the store contract suite and the adapter's own tests on a private server. The server's binaries come from `PATH` or from the directory `ROPESHIFT_TEST_PG_BIN` names. No solo binary imports the adapter, and `make check` holds that.

## Connections

The projects of an installation share one pool of connections, and the installation has a pool of its own. `Options.MaxConns` bounds each of them, and neither is ever smaller than 2. The two are separate because one pool deadlocked: a project's transaction holds a connection while it reads the installation's records through a second one, so enough writers on one pool each held a connection and none got the second.

A project's write holds one connection of its pool, and at most one of the installation's. The installation's side of a write begins at its first statement, read-only, and a write that registers a repository swaps it for a writing one at that point. The side commits, or ends if it only read, when the write's function returns and before the project's transaction commits.

## Idle transactions

Every connection sets `idle_in_transaction_session_timeout`, 30 seconds unless `Options.IdleInTransaction` says otherwise, so a process stopped inside a transaction cannot stall every writer of its project. The two transactions of a write are idle in turn: the project's while the installation's side runs a statement, and the side's while the project runs one.

A project statement that runs longer than the timeout between two uses of the installation's side ends the side's session, and the write fails. The same holds the other way: a statement of the side that waits for a lock, such as the repository index's, can outlast the timeout of the project's session. Writes are short by design, because the project's revision lock is taken first (ADR-0010). A deployment that runs a long statement in a write sets `IdleInTransaction` above the longest one, or turns the timeout off with a negative value.
