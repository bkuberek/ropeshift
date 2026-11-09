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

// SQLiteFile is the solo-mode backend: a file in a temporary directory.
var SQLiteFile = Backend{Name: "sqlite", Fresh: func(t testing.TB) Database {
	file := filepath.Join(t.TempDir(), "ledger.db")
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

// SQLiteMemory is the in-memory backend every fake Store uses (D6).
var SQLiteMemory = Backend{Name: "memory", Fresh: func(t testing.TB) Database {
	offset := &atomic.Int64{}
	db, err := sqlite.OpenMemoryDB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
