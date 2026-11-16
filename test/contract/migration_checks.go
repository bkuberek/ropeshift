package contract

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
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
	migrate func(ctx context.Context, db *sql.DB, dialect sqlstore.Dialect, schema string) error
}

var migrationSets = []migrationSet{
	{
		name:    "ledger",
		shipped: sqlstore.Dialect.Migrations,
		migrate: func(ctx context.Context, db *sql.DB, dialect sqlstore.Dialect, schema string) error {
			return sqlstore.New(db, dialect, sqlstore.Options{Schema: schema}).Migrate(ctx)
		},
	},
	{
		name:    "installation",
		shipped: sqlstore.Dialect.InstallationMigrations,
		migrate: func(ctx context.Context, db *sql.DB, dialect sqlstore.Dialect, schema string) error {
			return sqlstore.NewInstallation(db, dialect, sqlstore.Options{Schema: schema}).Migrate(ctx)
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
		err := r.set.migrate(r.t.Context(), r.db.DB, shippedDialect{r.db.Dialect, firstEdited}, r.db.Schema)
		r.wantRefused(err, sqlstore.MigrationError{
			Set: r.set.name, Version: shipped[0].Version, File: shipped[0].Name + ".sql",
			Recorded: checksum(shipped[0].SQL), Shipped: checksum(edited[0].SQL), Err: sqlstore.ErrMigrationChanged,
		})
		r.wantApplied(shipped)
	}},
	{"a migration this build no longer ships refuses to start", func(r migrationRun) {
		r.migrate(r.db.Dialect)
		shipped := r.shipped(r.db.Dialect)
		last := shipped[len(shipped)-1]
		err := r.set.migrate(r.t.Context(), r.db.DB, shippedDialect{r.db.Dialect, withoutLast}, r.db.Schema)
		r.wantRefused(err, sqlstore.MigrationError{
			Set: r.set.name, Version: last.Version, File: last.Name + ".sql",
			Recorded: checksum(last.SQL), Err: sqlstore.ErrMigrationNotShipped,
		})
		r.wantApplied(shipped)
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

func (r migrationRun) migrate(dialect sqlstore.Dialect) {
	r.t.Helper()
	if err := r.set.migrate(r.t.Context(), r.db.DB, dialect, r.db.Schema); err != nil {
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

// wantApplied holds that the database recorded each shipped migration, with its checksum, and no other.
func (r migrationRun) wantApplied(shipped []sqlstore.Migration) {
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
	want := make([]appliedMigration, 0, len(shipped))
	for _, m := range shipped {
		want = append(want, appliedMigration{version: m.Version, name: m.Name, checksum: checksum(m.SQL)})
	}
	if !slices.Equal(got, want) {
		r.t.Errorf("the %s database recorded\n%v\nwant\n%v", r.set.name, got, want)
	}
}

// wantRefused holds that a start refused with want, and that its message names the set, the version,
// the file, and both checksums.
func (r migrationRun) wantRefused(err error, want sqlstore.MigrationError) {
	r.t.Helper()
	if !errors.Is(err, want.Err) {
		r.t.Fatalf("the start returned %v, want %v", err, want.Err)
	}
	var refused *sqlstore.MigrationError
	if !errors.As(err, &refused) {
		r.t.Fatalf("the start returned %T, want a *sqlstore.MigrationError", err)
	}
	if *refused != want {
		r.t.Errorf("the start refused %+v, want %+v", *refused, want)
	}
	for _, part := range []string{want.Set + " migration", strconv.FormatInt(want.Version, 10), want.File, want.Recorded, want.Shipped} {
		if !strings.Contains(err.Error(), part) {
			r.t.Errorf("the refusal %q does not name %q", err, part)
		}
	}
}
