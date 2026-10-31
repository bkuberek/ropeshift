//go:build postgres

package pgtest

import (
	"strconv"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
)

// Dialect is Postgres's side of the Store's statements. Phase 1 ships no Postgres
// adapter (D7), so this lives with the tests and stays out of every binary. The
// schema so far is neutral, so it reuses the SQLite migration files.
type Dialect struct{}

// Placeholder renders a numbered parameter.
func (Dialect) Placeholder(position int) string { return "$" + strconv.Itoa(position) }

// ClockSQL must use clock_timestamp: the SQL-standard functions return the start
// of the transaction, which is stale after a wait for the workspace lock.
func (Dialect) ClockSQL() string {
	return "SELECT CAST(floor(extract(epoch FROM clock_timestamp()) * 1000) AS BIGINT)"
}

// Migrations reads the neutral schema.
func (Dialect) Migrations() ([]sqlstore.Migration, error) {
	return sqlite.Migrations()
}
