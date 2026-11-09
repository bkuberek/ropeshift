//go:build postgres

package pgtest

import (
	"embed"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
)

// Dialect is Postgres's side of the Store's statements. Phase 1 ships no Postgres
// adapter (D7), so this lives with the tests and stays out of every binary. It runs the
// neutral schema and its own search index.
type Dialect struct{}

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Placeholder renders a numbered parameter.
func (Dialect) Placeholder(position int) string { return "$" + strconv.Itoa(position) }

// ClockSQL must use clock_timestamp: the SQL-standard functions return the start
// of the transaction, which is stale after a wait for the workspace lock.
func (Dialect) ClockSQL() string {
	return "SELECT CAST(floor(extract(epoch FROM clock_timestamp()) * 1000) AS BIGINT)"
}

// Migrations reads the neutral schema and this dialect's own.
func (Dialect) Migrations() ([]sqlstore.Migration, error) {
	neutral, err := sqlite.NeutralMigrations()
	if err != nil {
		return nil, err
	}
	own, err := sqlite.ReadMigrations(migrationFiles, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	return sqlstore.MergeMigrations(neutral, own)
}

// MatchSQL ranks with tsvector and tsquery, as the design's storage table says.
func (Dialect) MatchSQL() string {
	return `SELECT d.workspace_id, d.kind, d.record_id,
       CAST(ts_rank(to_tsvector('simple', d.body), to_tsquery('simple', CAST(:query AS TEXT))) AS DOUBLE PRECISION) AS score
  FROM search_documents d
 WHERE to_tsvector('simple', d.body) @@ to_tsquery('simple', CAST(:query AS TEXT))`
}

// MatchQuery joins the terms with OR. A term holds only letters and digits, so quoting it
// is enough.
func (Dialect) MatchQuery(terms []string) string {
	quoted := make([]string, len(terms))
	for i, term := range terms {
		quoted[i] = "'" + term + "'"
	}
	return strings.Join(quoted, " | ")
}
