package contract

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
)

// Under -race the store's migrations cost far more than most checks, and every rig ran
// them again. A rig's database now starts as a copy of one the migrations ran on once per
// test binary, and the rig's first handle still calls Migrate, which finds every migration
// applied: the path a restarted store takes. The migration checks and TestSchemaShape
// still migrate from an empty database.

// migratedMemory is the image of an in-memory store every migration has run on.
var migratedMemory = sync.OnceValues(func() ([]byte, error) {
	ctx := context.Background()
	db, err := sqlite.OpenMemoryDB()
	if err != nil {
		return nil, err
	}
	store := sqlstore.New(db, sqlite.Dialect{}, sqlstore.Options{})
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		return nil, err
	}
	var image []byte
	err = onConnection(ctx, db, func(c imageConn) error {
		var err error
		image, err = c.Serialize()
		return err
	})
	return image, err
})

// migratedFile is the content of a ledger file every migration has run on, through the
// connection settings a solo-mode ledger opens with.
var migratedFile = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "contract-ledger-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, "ledger.db")
	store, err := sqlite.Open(file, sqlstore.Options{})
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(context.Background()); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := store.Close(); err != nil {
		return nil, err
	}
	// The last connection to close moves the write-ahead log into the file and removes
	// the log, so the file alone holds the migrated database.
	if _, err := os.Stat(file + "-wal"); !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the migrated ledger kept a write-ahead log: %v", err)
	}
	return os.ReadFile(file)
})

// migratedInstallation is the content of an installation database every installation migration has
// run on.
var migratedInstallation = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "contract-installation-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, sqlite.InstallationFileName)
	installation, err := sqlite.OpenInstallation(file, sqlstore.Options{})
	if err != nil {
		return nil, err
	}
	if err := installation.Migrate(context.Background()); err != nil {
		_ = installation.Close()
		return nil, err
	}
	if err := installation.Close(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(file + "-wal"); !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the migrated installation kept a write-ahead log: %v", err)
	}
	return os.ReadFile(file)
})

// writeMigratedInstallation writes a migrated installation database at a path that holds no file yet.
func writeMigratedInstallation(file string) error {
	image, err := migratedInstallation()
	if err != nil {
		return err
	}
	return os.WriteFile(file, image, 0o644)
}

// restoreMigrated fills a new in-memory database with the migrated image.
func restoreMigrated(db *sql.DB) error {
	image, err := migratedMemory()
	if err != nil {
		return err
	}
	return onConnection(context.Background(), db, func(c imageConn) error { return c.Deserialize(image) })
}

// writeMigrated writes a migrated ledger file at a path that holds no file yet, with the
// mode SQLite creates a database file with.
func writeMigrated(file string) error {
	image, err := migratedFile()
	if err != nil {
		return err
	}
	return os.WriteFile(file, image, 0o644)
}

// imageConn is the SQLite driver's connection, which reads and writes a whole database image.
type imageConn interface {
	Serialize() ([]byte, error)
	Deserialize([]byte) error
}

// onConnection runs a function on the one connection an in-memory database lives on.
func onConnection(ctx context.Context, db *sql.DB, f func(imageConn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return conn.Raw(func(driverConn any) error {
		c, ok := driverConn.(imageConn)
		if !ok {
			return fmt.Errorf("the driver connection %T cannot copy a database image", driverConn)
		}
		return f(c)
	})
}
