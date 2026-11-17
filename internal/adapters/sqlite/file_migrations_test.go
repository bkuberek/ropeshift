package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
)

// fileMigrator is a database the worker keeps for itself, which records its schema files by name.
type fileMigrator interface {
	Migrate(ctx context.Context) error
	MigrateFrom(ctx context.Context, files fs.FS) error
	Close() error
}

// fileSet is the journal or the telemetry store, with the files this build ships for it.
type fileSet struct {
	name  string
	table string
	files fs.FS
	// open opens a database file whose migrations warn to warn.
	open func(file string, warn func(string)) (fileMigrator, error)
}

var fileSets = []fileSet{
	{"journal", "journal_migrations", sqlite.JournalFiles, func(file string, warn func(string)) (fileMigrator, error) {
		journal, err := sqlite.OpenJournal(file)
		if err != nil {
			return nil, err
		}
		journal.Warn = warn
		return journal, nil
	}},
	{"telemetry", "telemetry_migrations", sqlite.TelemetryFiles, func(file string, warn func(string)) (fileMigrator, error) {
		store, err := sqlite.OpenTelemetry(file)
		if err != nil {
			return nil, err
		}
		store.Warn = warn
		return store, nil
	}},
}

// fileRun is one check of one set on a new database file.
type fileRun struct {
	t        *testing.T
	set      fileSet
	db       *sql.DB
	migrator fileMigrator
	// shipped is the text of each file this build ships, by its name.
	shipped map[string]string
	// warnings are what the migrations warned, in order.
	warnings *[]string
}

var fileChecks = []struct {
	name string
	run  func(r fileRun)
}{
	{"a fresh database records the checksum of each file", func(r fileRun) {
		r.migrate()
		r.wantApplied(r.shipped)
	}},
	{"a database from before checksums gets the column and the checksum of each file", func(r fileRun) {
		r.migrate()
		if _, err := r.db.ExecContext(r.t.Context(), "ALTER TABLE "+r.set.table+" DROP COLUMN checksum"); err != nil {
			r.t.Fatal(err)
		}
		r.migrate()
		r.wantApplied(r.shipped)
	}},
	{"an unchanged restart applies nothing and keeps each checksum", func(r fileRun) {
		for range 3 {
			r.migrate()
		}
		r.wantApplied(r.shipped)
	}},
	{"a changed applied file refuses to start", func(r fileRun) {
		r.migrate()
		first := slices.Sorted(maps.Keys(r.shipped))[0]
		edited := maps.Clone(r.shipped)
		edited[first] += "\n-- edited after it was applied\n"
		err := r.migrator.MigrateFrom(r.t.Context(), mapFS(edited))
		r.wantRefused(err, sqlstore.MigrationError{
			Set: r.set.name, Version: version(r.t, first), File: path.Base(first),
			Recorded: checksum(r.shipped[first]), Shipped: checksum(edited[first]),
		})
		r.wantApplied(r.shipped)
	}},
	{"a refusal applies no file that is pending", func(r fileRun) {
		r.migrate()
		first := slices.Sorted(maps.Keys(r.shipped))[0]
		edited := r.later()
		edited[first] += "\n-- edited after it was applied\n"
		err := r.migrator.MigrateFrom(r.t.Context(), mapFS(edited))
		r.wantRefused(err, sqlstore.MigrationError{
			Set: r.set.name, Version: version(r.t, first), File: path.Base(first),
			Recorded: checksum(r.shipped[first]), Shipped: checksum(edited[first]),
		})
		r.wantApplied(r.shipped)
	}},
	{"a refusal records no checksum of a file from before checksums", func(r fileRun) {
		// The later files make two applied, as the telemetry store ships one.
		later := r.later()
		if err := r.migrator.MigrateFrom(r.t.Context(), mapFS(later)); err != nil {
			r.t.Fatal(err)
		}
		names := slices.Sorted(maps.Keys(later))
		first, last := names[0], names[len(names)-1]
		if _, err := r.db.ExecContext(r.t.Context(), "UPDATE "+r.set.table+" SET checksum = NULL WHERE name = ?1", last); err != nil {
			r.t.Fatal(err)
		}
		edited := maps.Clone(later)
		edited[first] += "\n-- edited after it was applied\n"
		err := r.migrator.MigrateFrom(r.t.Context(), mapFS(edited))
		r.wantRefused(err, sqlstore.MigrationError{
			Set: r.set.name, Version: version(r.t, first), File: path.Base(first),
			Recorded: checksum(later[first]), Shipped: checksum(edited[first]),
		})
		want := checksums(later)
		want[last] = ""
		r.wantRecorded(want)
	}},
	{"a database a later build migrated warns once and starts", func(r fileRun) {
		later := r.later()
		if err := r.migrator.MigrateFrom(r.t.Context(), mapFS(later)); err != nil {
			r.t.Fatal(err)
		}
		*r.warnings = nil
		if err := r.migrator.Migrate(r.t.Context()); err != nil {
			r.t.Fatalf("the start returned %v, want it to start", err)
		}
		if len(*r.warnings) != 1 {
			r.t.Fatalf("the start warned %q, want one warning", *r.warnings)
		}
		warning := (*r.warnings)[0]
		parts := []string{r.set.name}
		for name := range later {
			if _, ok := r.shipped[name]; !ok {
				parts = append(parts, strconv.FormatInt(version(r.t, name), 10), path.Base(name))
			}
		}
		for _, part := range parts {
			if !strings.Contains(warning, part) {
				r.t.Errorf("the warning %q does not name %q", warning, part)
			}
		}
		r.wantApplied(later)
	}},
}

func TestFileMigrationChecksums(t *testing.T) {
	t.Parallel()
	for _, set := range fileSets {
		for _, check := range fileChecks {
			t.Run(set.name+"/"+check.name, func(t *testing.T) {
				t.Parallel()
				file := filepath.Join(t.TempDir(), set.name+".db")
				var warnings []string
				migrator, err := set.open(file, func(warning string) { warnings = append(warnings, warning) })
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = migrator.Close() })
				db, err := sqlite.OpenDB(file)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				check.run(fileRun{t: t, set: set, db: db, migrator: migrator, shipped: shippedFiles(t, set), warnings: &warnings})
			})
		}
	}
}

// shippedFiles reads the text of each file this build ships for a set.
func shippedFiles(t *testing.T, set fileSet) map[string]string {
	t.Helper()
	names, err := fs.Glob(set.files, set.table+"/*.sql")
	if err != nil || len(names) == 0 {
		t.Fatalf("the %s ships %v: %v", set.name, names, err)
	}
	out := map[string]string{}
	for _, name := range names {
		text, err := fs.ReadFile(set.files, name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = string(text)
	}
	return out
}

func mapFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, text := range files {
		out[name] = &fstest.MapFile{Data: []byte(text)}
	}
	return out
}

// version reads the version a file's name starts with.
func version(t *testing.T, name string) int64 {
	t.Helper()
	number, _, _ := strings.Cut(path.Base(name), "_")
	v, err := strconv.ParseInt(number, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// checksum is the SHA-256 of a file's text, in hex, computed apart from the store.
func checksum(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// later is the files a later build ships: this build's, and two past the newest.
func (r fileRun) later() map[string]string {
	out := maps.Clone(r.shipped)
	names := slices.Sorted(maps.Keys(r.shipped))
	newest := version(r.t, names[len(names)-1])
	for v := newest + 1; v <= newest+2; v++ {
		out[fmt.Sprintf("%s/%04d_later.sql", r.set.table, v)] = fmt.Sprintf("CREATE TABLE later_%d (id INTEGER);\n", v)
	}
	return out
}

// checksums is the checksum of each file's text, by its name.
func checksums(files map[string]string) map[string]string {
	out := make(map[string]string, len(files))
	for name, text := range files {
		out[name] = checksum(text)
	}
	return out
}

// migrate migrates with the files this build ships.
func (r fileRun) migrate() {
	r.t.Helper()
	if err := r.migrator.Migrate(r.t.Context()); err != nil {
		r.t.Fatalf("migrate: %v", err)
	}
}

// wantApplied holds that the database recorded each file, with its checksum, and no other.
func (r fileRun) wantApplied(files map[string]string) {
	r.t.Helper()
	r.wantRecorded(checksums(files))
}

// wantRecorded holds that the database recorded exactly the files want names, each with its checksum,
// where an empty checksum is one the database recorded as NULL.
func (r fileRun) wantRecorded(want map[string]string) {
	r.t.Helper()
	rows, err := r.db.QueryContext(r.t.Context(), "SELECT name, checksum FROM "+r.set.table+" ORDER BY name")
	if err != nil {
		r.t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var name string
		var sum sql.NullString
		if err := rows.Scan(&name, &sum); err != nil {
			r.t.Fatal(err)
		}
		got[name] = sum.String
	}
	if err := rows.Err(); err != nil {
		r.t.Fatal(err)
	}
	if !maps.Equal(got, want) {
		r.t.Errorf("the %s recorded\n%v\nwant\n%v", r.set.name, got, want)
	}
}

// wantRefused holds that a start refused with want, that its message names the set, the version, the
// file, and both checksums, and that it says what a person does next.
func (r fileRun) wantRefused(err error, want sqlstore.MigrationError) {
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
	for _, part := range []string{
		want.Set + " migration", strconv.FormatInt(want.Version, 10), want.File, want.Recorded, want.Shipped,
		"the build that migrated", "restore the state directory from a backup", "docs/troubleshooting.md#database-refuses-to-start",
	} {
		if !strings.Contains(err.Error(), part) {
			r.t.Errorf("the refusal %q does not name %q", err, part)
		}
	}
}
