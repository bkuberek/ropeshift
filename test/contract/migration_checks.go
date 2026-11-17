package contract

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
)

// MigrationBackend makes an empty database for each migration check.
type MigrationBackend struct {
	Name  string
	Fresh func(t testing.TB) MigrationDatabase
}

// MigrationDatabase is one empty database, which the backend closes when the test ends.
type MigrationDatabase struct {
	DB      *sql.DB
	Dialect sqlstore.Dialect
	// Schema is where the stores keep their tables, or empty for the database's own default.
	Schema string
}

// migrationSet is one set of migrations the store's runner applies: a project's ledger, or the
// installation database.
type migrationSet struct {
	name    string
	shipped func(sqlstore.Dialect) ([]sqlstore.Migration, error)
	migrate func(ctx context.Context, db *sql.DB, dialect sqlstore.Dialect, opts sqlstore.Options) error
}

var migrationSets = []migrationSet{
	{
		name:    "ledger",
		shipped: sqlstore.Dialect.Migrations,
		migrate: func(ctx context.Context, db *sql.DB, dialect sqlstore.Dialect, opts sqlstore.Options) error {
			return sqlstore.New(db, dialect, opts).Migrate(ctx)
		},
	},
	{
		name:    "installation",
		shipped: sqlstore.Dialect.InstallationMigrations,
		migrate: func(ctx context.Context, db *sql.DB, dialect sqlstore.Dialect, opts sqlstore.Options) error {
			return sqlstore.NewInstallation(db, dialect, opts).Migrate(ctx)
		},
	},
}

// shippedDialect ships what edit makes of a dialect's migrations, as another build would.
type shippedDialect struct {
	sqlstore.Dialect
	edit func([]sqlstore.Migration) []sqlstore.Migration
}

func (d shippedDialect) Migrations() ([]sqlstore.Migration, error) {
	return d.edited(d.Dialect.Migrations())
}

func (d shippedDialect) InstallationMigrations() ([]sqlstore.Migration, error) {
	return d.edited(d.Dialect.InstallationMigrations())
}

func (d shippedDialect) edited(migrations []sqlstore.Migration, err error) ([]sqlstore.Migration, error) {
	if err != nil {
		return nil, err
	}
	return d.edit(migrations), nil
}

// withoutLast ships every migration but the newest.
func withoutLast(migrations []sqlstore.Migration) []sqlstore.Migration {
	return migrations[:len(migrations)-1]
}

// firstEdited ships the oldest migration with a line added after a database applied it.
func firstEdited(migrations []sqlstore.Migration) []sqlstore.Migration {
	migrations[0].SQL += "\n-- edited after it was applied\n"
	return migrations
}

// withLater ships two migrations past the newest, as a later build does.
func withLater(migrations []sqlstore.Migration) []sqlstore.Migration {
	newest := slices.MaxFunc(migrations, func(a, b sqlstore.Migration) int { return int(a.Version - b.Version) }).Version
	for version := newest + 1; version <= newest+2; version++ {
		migrations = append(migrations, sqlstore.Migration{
			Version: version, Name: fmt.Sprintf("%04d_later", version), SQL: fmt.Sprintf("CREATE TABLE later_%d (id BIGINT)", version),
		})
	}
	return migrations
}

// editedWithLater ships the oldest migration edited and two migrations past the newest.
func editedWithLater(migrations []sqlstore.Migration) []sqlstore.Migration {
	return withLater(firstEdited(migrations))
}

// appliedMigration is one row of schema_migrations, as a check compares it.
type appliedMigration struct {
	version  int64
	name     string
	checksum string
}

// migrationRun is one check of one set on one empty database.
type migrationRun struct {
	t   *testing.T
	set migrationSet
	db  MigrationDatabase
}

var migrationChecks = []struct {
	name string
	run  func(r migrationRun)
}{
	{"a ledger from before the identity marker marks each repository it holds as legacy, a repository this build writes is not, and one an older build writes without the column is", func(r migrationRun) {
		if r.set.name != "ledger" {
			return
		}
		r.migrate(shippedDialect{r.db.Dialect, func(migrations []sqlstore.Migration) []sqlstore.Migration {
			return slices.DeleteFunc(migrations, func(m sqlstore.Migration) bool { return m.Name == identityMarkerMigration })
		}})
		table := r.set.qualify(r.db, "repositories")
		r.exec("INSERT INTO " + table + " (workspace_id, id) VALUES ('w', 'old')")
		r.migrate(r.db.Dialect)
		// This build names the column and writes false. A build from before the column does not name it, as after
		// a downgrade, and its record is legacy, which keeps the match by another root that it relies on.
		r.exec("INSERT INTO " + table + " (workspace_id, id, identity_legacy) VALUES ('w', 'new', 0)")
		r.exec("INSERT INTO " + table + " (workspace_id, id) VALUES ('w', 'older-build')")
		for id, want := range map[string]int64{"old": 1, "new": 0, "older-build": 1} {
			var got int64
			if err := r.db.DB.QueryRowContext(r.t.Context(), "SELECT identity_legacy FROM "+table+" WHERE id = '"+id+"'").Scan(&got); err != nil {
				r.t.Fatal(err)
			}
			if got != want {
				r.t.Errorf("repository %s has identity_legacy %d, want %d", id, got, want)
			}
		}
	}},
	{"a fresh database records the checksum of each migration", func(r migrationRun) {
		r.migrate(r.db.Dialect)
		r.wantApplied(r.shipped(r.db.Dialect))
	}},
	{"a database from before checksums gets the column and the checksum of each migration", func(r migrationRun) {
		r.migrate(shippedDialect{r.db.Dialect, withoutLast})
		r.exec("ALTER TABLE " + r.table() + " DROP COLUMN checksum")
		r.migrate(r.db.Dialect)
		r.wantApplied(r.shipped(r.db.Dialect))
	}},
	{"an unchanged restart applies nothing and keeps each checksum", func(r migrationRun) {
		for range 3 {
			r.migrate(r.db.Dialect)
		}
		r.wantApplied(r.shipped(r.db.Dialect))
	}},
	{"a changed applied migration refuses to start", func(r migrationRun) {
		r.migrate(r.db.Dialect)
		shipped := r.shipped(r.db.Dialect)
		edited := r.shipped(shippedDialect{r.db.Dialect, firstEdited})
		err := r.set.migrate(r.t.Context(), r.db.DB, shippedDialect{r.db.Dialect, firstEdited}, r.options())
		r.wantRefused(err, sqlstore.MigrationError{
			Set: r.set.name, Version: shipped[0].Version, File: shipped[0].Name + ".sql",
			Recorded: checksum(shipped[0].SQL), Shipped: checksum(edited[0].SQL),
		})
		r.wantApplied(shipped)
	}},
	{"a refusal applies no migration that is pending", func(r migrationRun) {
		r.migrate(r.db.Dialect)
		shipped := r.shipped(r.db.Dialect)
		edited := r.shipped(shippedDialect{r.db.Dialect, firstEdited})
		err := r.set.migrate(r.t.Context(), r.db.DB, shippedDialect{r.db.Dialect, editedWithLater}, r.options())
		r.wantRefused(err, sqlstore.MigrationError{
			Set: r.set.name, Version: shipped[0].Version, File: shipped[0].Name + ".sql",
			Recorded: checksum(shipped[0].SQL), Shipped: checksum(edited[0].SQL),
		})
		r.wantApplied(shipped)
	}},
	{"a refusal records no checksum of a migration from before checksums", func(r migrationRun) {
		r.migrate(r.db.Dialect)
		shipped := r.shipped(r.db.Dialect)
		last := shipped[len(shipped)-1]
		r.exec(fmt.Sprintf("UPDATE %s SET checksum = NULL WHERE version = %d", r.table(), last.Version))
		edited := r.shipped(shippedDialect{r.db.Dialect, firstEdited})
		err := r.set.migrate(r.t.Context(), r.db.DB, shippedDialect{r.db.Dialect, firstEdited}, r.options())
		r.wantRefused(err, sqlstore.MigrationError{
			Set: r.set.name, Version: shipped[0].Version, File: shipped[0].Name + ".sql",
			Recorded: checksum(shipped[0].SQL), Shipped: checksum(edited[0].SQL),
		})
		want := recordsOf(shipped)
		want[len(want)-1].checksum = ""
		r.wantRecorded(want)
	}},
	{"a database a later build migrated warns once and starts", func(r migrationRun) {
		r.migrate(shippedDialect{r.db.Dialect, withLater})
		later := r.shipped(shippedDialect{r.db.Dialect, withLater})
		var warnings []string
		opts := r.options()
		opts.Warn = func(warning string) { warnings = append(warnings, warning) }
		if err := r.set.migrate(r.t.Context(), r.db.DB, r.db.Dialect, opts); err != nil {
			r.t.Fatalf("the start returned %v, want it to start", err)
		}
		if len(warnings) != 1 {
			r.t.Fatalf("the start warned %q, want one warning", warnings)
		}
		parts := []string{r.set.name}
		for _, m := range later[len(later)-2:] {
			parts = append(parts, strconv.FormatInt(m.Version, 10), m.Name+".sql")
		}
		for _, part := range parts {
			if !strings.Contains(warnings[0], part) {
				r.t.Errorf("the warning %q does not name %q", warnings[0], part)
			}
		}
		r.wantApplied(later)
	}},
}

// RunMigrationChecks runs each check of the checksums the store's runner records on each set of
// migrations it applies, each on an empty database.
func RunMigrationChecks(t *testing.T, backend MigrationBackend) {
	t.Helper()
	for _, set := range migrationSets {
		for _, check := range migrationChecks {
			t.Run(backend.Name+"/"+set.name+"/"+check.name, func(t *testing.T) {
				t.Parallel()
				check.run(migrationRun{t: t, set: set, db: backend.Fresh(t)})
			})
		}
	}
}

// checksum is the SHA-256 of a migration's text, in hex, computed apart from the store.
func checksum(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// options are the stores' options on the database.
func (r migrationRun) options() sqlstore.Options {
	return sqlstore.Options{Schema: r.db.Schema}
}

func (r migrationRun) migrate(dialect sqlstore.Dialect) {
	r.t.Helper()
	if err := r.set.migrate(r.t.Context(), r.db.DB, dialect, r.options()); err != nil {
		r.t.Fatalf("migrate: %v", err)
	}
}

func (r migrationRun) shipped(dialect sqlstore.Dialect) []sqlstore.Migration {
	r.t.Helper()
	migrations, err := r.set.shipped(dialect)
	if err != nil {
		r.t.Fatal(err)
	}
	slices.SortFunc(migrations, func(a, b sqlstore.Migration) int { return int(a.Version - b.Version) })
	return migrations
}

// identityMarkerMigration is the ledger migration that marks the records made before a repository's identity
// was its first-parent root.
const identityMarkerMigration = "0110_repository_identity_legacy"

// qualify names a table in the database's schema.
func (s migrationSet) qualify(db MigrationDatabase, table string) string {
	if db.Schema == "" {
		return table
	}
	return `"` + strings.ReplaceAll(db.Schema, `"`, `""`) + `".` + table
}

// table names schema_migrations in the database's schema.
func (r migrationRun) table() string {
	if r.db.Schema == "" {
		return "schema_migrations"
	}
	return `"` + strings.ReplaceAll(r.db.Schema, `"`, `""`) + `".schema_migrations`
}

func (r migrationRun) exec(statement string) {
	r.t.Helper()
	if _, err := r.db.DB.ExecContext(r.t.Context(), statement); err != nil {
		r.t.Fatalf("%s: %v", statement, err)
	}
}

// recordsOf is the row each shipped migration records, with its checksum.
func recordsOf(shipped []sqlstore.Migration) []appliedMigration {
	out := make([]appliedMigration, 0, len(shipped))
	for _, m := range shipped {
		out = append(out, appliedMigration{version: m.Version, name: m.Name, checksum: checksum(m.SQL)})
	}
	return out
}

// wantApplied holds that the database recorded each shipped migration, with its checksum, and no other.
func (r migrationRun) wantApplied(shipped []sqlstore.Migration) {
	r.t.Helper()
	r.wantRecorded(recordsOf(shipped))
}

// wantRecorded holds that schema_migrations holds exactly the rows want lists, in version order, where an
// empty checksum is one the database recorded as NULL.
func (r migrationRun) wantRecorded(want []appliedMigration) {
	r.t.Helper()
	rows, err := r.db.DB.QueryContext(r.t.Context(), "SELECT version, name, checksum FROM "+r.table()+" ORDER BY version")
	if err != nil {
		r.t.Fatal(err)
	}
	defer rows.Close()
	var got []appliedMigration
	for rows.Next() {
		var row appliedMigration
		var sum sql.NullString
		if err := rows.Scan(&row.version, &row.name, &sum); err != nil {
			r.t.Fatal(err)
		}
		row.checksum = sum.String
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		r.t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		r.t.Errorf("the %s database recorded\n%v\nwant\n%v", r.set.name, got, want)
	}
}

// wantRefused holds that a start refused with want, that its message names the set, the version, the
// file, and both checksums, and that it says what a person does next.
func (r migrationRun) wantRefused(err error, want sqlstore.MigrationError) {
	r.t.Helper()
	if !errors.Is(err, sqlstore.ErrMigrationChanged) {
		r.t.Fatalf("the start returned %v, want %v", err, sqlstore.ErrMigrationChanged)
	}
	var refused *sqlstore.MigrationError
	if !errors.As(err, &refused) {
		r.t.Fatalf("the start returned %T, want a *sqlstore.MigrationError", err)
	}
	if *refused != want {
		r.t.Errorf("the start refused %+v, want %+v", *refused, want)
	}
	for _, part := range append([]string{want.Set + " migration", strconv.FormatInt(want.Version, 10), want.File, want.Recorded, want.Shipped}, refusalNextStep...) {
		if !strings.Contains(err.Error(), part) {
			r.t.Errorf("the refusal %q does not name %q", err, part)
		}
	}
}

// refusalNextStep is what every refusal tells a person to do: run the build that migrated the database,
// or restore it, as the user docs' troubleshooting page says.
var refusalNextStep = []string{"the build that migrated", "restore the state directory from a backup", "docs/troubleshooting.md#database-refuses-to-start"}
