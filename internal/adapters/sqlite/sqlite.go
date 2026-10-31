// Package sqlite is the solo-mode Store adapter: the statements of sqlstore on a
// pure-Go SQLite file, or on an in-memory database (D6).
package sqlite

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"

	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"

	// The driver registers itself as "sqlite".
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// busyMillis is how long a writer queues for the write lock. A transaction stays
// short (R17), so a wait this long means something stopped inside one.
const busyMillis = 30000

// poolSize bounds the connections of one process on a file. SQLite has one
// writer, so more connections only add readers.
const poolSize = 8

// Dialect is SQLite's side of the Store's statements.
type Dialect struct{}

// Placeholder renders a numbered parameter, which a repeated name can reuse.
func (Dialect) Placeholder(position int) string { return "?" + strconv.Itoa(position) }

// ClockSQL reads the host clock, because SQLite has no server (ADR-0010).
func (Dialect) ClockSQL() string {
	return "SELECT CAST(unixepoch('subsec') * 1000 AS INTEGER)"
}

// Migrations reads the embedded schema files.
func (Dialect) Migrations() ([]sqlstore.Migration, error) {
	return Migrations()
}

// Migrations lists the embedded schema files, in version order.
func Migrations() ([]sqlstore.Migration, error) {
	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	out := make([]sqlstore.Migration, 0, len(names))
	for _, name := range names {
		base := strings.TrimSuffix(path.Base(name), ".sql")
		number, _, ok := strings.Cut(base, "_")
		if !ok {
			return nil, fmt.Errorf("migration %s has no version", name)
		}
		version, err := strconv.ParseInt(number, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", name, err)
		}
		text, err := migrationFiles.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out = append(out, sqlstore.Migration{Version: version, Name: base, SQL: string(text)})
	}
	return out, nil
}

// fileDSN keeps writers in BEGIN IMMEDIATE, so a transaction never fails upgrading
// from read to write. synchronous(FULL) keeps every commit it acknowledged.
func fileDSN(file string) string {
	return "file:" + file + "?_txlock=immediate" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(FULL)" +
		"&_pragma=busy_timeout(" + strconv.Itoa(busyMillis) + ")"
}

// OpenDB opens a SQLite file.
func OpenDB(file string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", fileDSN(file))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(poolSize)
	db.SetMaxIdleConns(poolSize)
	return db, nil
}

// OpenMemoryDB opens an in-memory database. It lives on one connection, so that
// connection must never be recycled and every call shares it in turn.
func OpenMemoryDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file::memory:?_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	return db, nil
}

// Open returns a Store on a SQLite file.
func Open(file string, opts sqlstore.Options) (*sqlstore.Store, error) {
	db, err := OpenDB(file)
	if err != nil {
		return nil, err
	}
	return sqlstore.New(db, Dialect{}, opts), nil
}

// OpenMemory returns a Store on a new in-memory database.
func OpenMemory(opts sqlstore.Options) (*sqlstore.Store, error) {
	db, err := OpenMemoryDB()
	if err != nil {
		return nil, err
	}
	return sqlstore.New(db, Dialect{}, opts), nil
}
