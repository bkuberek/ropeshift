package contract

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ports"
)

// ShiftedDialect adds an offset to database time, without changing a statement
// and with no table in the schema. It is how a check moves the clock.
type ShiftedDialect struct {
	sqlstore.Dialect
	Offset *atomic.Int64
}

// ClockSQL reads the wrapped clock plus the offset.
func (d ShiftedDialect) ClockSQL() string {
	inner := strings.TrimPrefix(d.Dialect.ClockSQL(), "SELECT ")
	return "SELECT (" + inner + ") + " + strconv.FormatInt(d.Offset.Load(), 10)
}

// SQLiteFile is the solo-mode backend: a file in a temporary directory, which starts as a
// copy of a migrated ledger.
var SQLiteFile = Backend{Name: "sqlite", Fresh: func(t testing.TB) Database {
	file := filepath.Join(t.TempDir(), "ledger.db")
	if err := writeMigrated(file); err != nil {
		t.Fatal(err)
	}
	offset := &atomic.Int64{}
	return Database{
		Restartable: true,
		ShiftClock:  func(delta int64) { offset.Add(delta) },
		Open: func(t testing.TB, opts sqlstore.Options) ports.Store {
			db, err := sqlite.OpenDB(file)
			if err != nil {
				t.Fatal(err)
			}
			store := sqlstore.New(db, ShiftedDialect{sqlite.Dialect{}, offset}, opts)
			t.Cleanup(func() { _ = store.Close() })
			return store
		},
		Damage: func(t testing.TB, statement string) error {
			db, err := sqlite.OpenDB(file)
			if err != nil {
				return err
			}
			defer db.Close()
			_, err = db.Exec(statement)
			return err
		},
	}
}}

// SQLiteMemory is the in-memory backend every fake Store uses (D6). Its database starts
// as the image of a migrated one.
var SQLiteMemory = Backend{Name: "memory", Fresh: func(t testing.TB) Database {
	offset := &atomic.Int64{}
	db, err := sqlite.OpenMemoryDB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := restoreMigrated(db); err != nil {
		t.Fatal(err)
	}
	var store *sqlstore.Store
	return Database{
		ShiftClock: func(delta int64) { offset.Add(delta) },
		Open: func(_ testing.TB, opts sqlstore.Options) ports.Store {
			if store == nil {
				store = sqlstore.New(db, ShiftedDialect{sqlite.Dialect{}, offset}, opts)
			}
			return store
		},
		Damage: func(_ testing.TB, statement string) error {
			_, err := db.Exec(statement)
			return err
		},
	}
}}

// SQLiteProject is a project of an installation (ADR-0021): a ledger file like SQLiteFile, whose
// members, workers, orchestrators, and autonomy live in an installation database beside it. Every
// check holds on it as it does on a single ledger.
var SQLiteProject = Backend{Name: "project", Fresh: func(t testing.TB) Database {
	dir := t.TempDir()
	file := filepath.Join(dir, "ledger.db")
	if err := writeMigrated(file); err != nil {
		t.Fatal(err)
	}
	if err := writeMigratedInstallation(filepath.Join(dir, sqlite.InstallationFileName)); err != nil {
		t.Fatal(err)
	}
	offset := &atomic.Int64{}
	return Database{
		Restartable: true,
		ShiftClock:  func(delta int64) { offset.Add(delta) },
		Open: func(t testing.TB, opts sqlstore.Options) ports.Store {
			// The installation reads the same shifted clock, because a run window it holds ends by that time.
			installationDB, err := sqlite.OpenDB(filepath.Join(dir, sqlite.InstallationFileName))
			if err != nil {
				t.Fatal(err)
			}
			installation := sqlstore.NewInstallation(installationDB, ShiftedDialect{sqlite.Dialect{}, offset}, sqlstore.Options{})
			t.Cleanup(func() { _ = installation.Close() })
			db, err := sqlite.OpenDB(file)
			if err != nil {
				t.Fatal(err)
			}
			opts.Installation = installation
			store := sqlstore.New(db, ShiftedDialect{sqlite.Dialect{}, offset}, opts)
			t.Cleanup(func() { _ = store.Close() })
			return store
		},
		Damage: func(t testing.TB, statement string) error {
			db, err := sqlite.OpenDB(file)
			if err != nil {
				return err
			}
			defer db.Close()
			_, err = db.Exec(statement)
			return err
		},
	}
}}
