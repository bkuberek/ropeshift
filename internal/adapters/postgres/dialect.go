package postgres

import (
	"embed"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
)

// Dialect is Postgres's side of the Store's statements. It runs the neutral schema and its own search
// index, which sit beside SQLite's counterpart at the same version.
type Dialect struct{}

// migrationFiles are Postgres's counterparts of the changes only one database can express. Each sits at
// the version of SQLite's file in internal/adapters/sqlite/dialect_migrations, and no neutral file
// holds that version.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// Placeholder renders a numbered parameter.
func (Dialect) Placeholder(position int) string { return "$" + strconv.Itoa(position) }

// ClockSQL must use clock_timestamp: the SQL-standard functions return the start
// of the transaction, which is stale after a wait for the workspace lock.
func (Dialect) ClockSQL() string {
	return "SELECT CAST(floor(extract(epoch FROM clock_timestamp()) * 1000) AS BIGINT)"
}

// ColumnsSQL reads the catalog in the schema the transaction's search path resolves to.
func (Dialect) ColumnsSQL() string {
	return `SELECT CAST(column_name AS TEXT) FROM information_schema.columns
 WHERE table_schema = current_schema() AND table_name = CAST(:table AS TEXT)`
}

// AddColumnSQL adds the column unless the table has it. A second start that read the columns before the
// first committed waits on the first's lock, and then finds the column there.
func (Dialect) AddColumnSQL(table, column string) string {
	return `ALTER TABLE ` + table + ` ADD COLUMN IF NOT EXISTS ` + column
}

// uniqueViolation is the SQLSTATE of a unique index that already holds a key.
const uniqueViolation = "23505"

// LostCreateRace reports a unique violation. Two transactions that make the same table at once each insert
// the table's type into the catalog, and the one that waits on the other's uncommitted row gets the violation
// when the other commits, though CREATE TABLE IF NOT EXISTS would now find the table there.
func (Dialect) LostCreateRace(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
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

// InstallationMigrations reads the installation database's neutral schema, which needs nothing of
// Postgres's own.
func (Dialect) InstallationMigrations() ([]sqlstore.Migration, error) {
	return sqlite.NeutralInstallationMigrations()
}

// SchemaSQL sets the search path for the rest of a transaction, so the neutral statements read and
// write one project's schema, or the installation's (ADR-0021). SET LOCAL ends with the transaction.
func (Dialect) SchemaSQL(schema string) string {
	return `SET LOCAL search_path TO "` + strings.ReplaceAll(schema, `"`, `""`) + `"`
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

var _ sqlstore.Dialect = Dialect{}
