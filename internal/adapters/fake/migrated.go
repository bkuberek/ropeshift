package fake

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
)

// Under the race detector, applying the schema costs a new store far more than most tests spend after it:
// each migration is a transaction of DDL on an instrumented SQLite. The schema is the same for every store,
// so a process migrates one in-memory database the first time a test asks and keeps its bytes. Each store
// then loads its own copy: the driver copies the bytes into memory the new connection owns and frees, so
// two copies share no connection, file, or memory, and the kept bytes never change. The migrations a copy
// records carry the time the process migrated the first. A test of the migrations themselves migrates an
// empty database instead.

// ledgerImage is the bytes of an in-memory ledger every migration has run on.
var ledgerImage = sync.OnceValues(func() ([]byte, error) {
	return migratedImage(func(ctx context.Context, db *sql.DB) error {
		return sqlstore.New(db, sqlite.Dialect{}, sqlstore.Options{}).Migrate(ctx)
	})
})

// installationImage is the bytes of an in-memory installation database every installation migration has
// run on.
var installationImage = sync.OnceValues(func() ([]byte, error) {
	return migratedImage(func(ctx context.Context, db *sql.DB) error {
		return sqlstore.NewInstallation(db, sqlite.Dialect{}, sqlstore.Options{}).Migrate(ctx)
	})
})

// SeedState writes migrated databases into a state directory that holds none yet, before
// sqlite.OpenState opens it: the installation database, and the ledger of each project named, where
// State.CreateProject puts it. OpenState and CreateProject still run whole, and find every migration
// applied. SQLite opens each file in the rollback journal it was written in and puts it in WAL mode, as
// it does a file it has just made.
func SeedState(dir string, projects ...ledger.WorkspaceID) error {
	installation, err := installationImage()
	if err != nil {
		return err
	}
	project, err := ledgerImage()
	if err != nil {
		return err
	}
	files := map[string][]byte{filepath.Join(dir, sqlite.InstallationFileName): installation}
	if len(projects) > 0 {
		if err := os.MkdirAll(filepath.Join(dir, sqlite.ProjectsDir), 0o700); err != nil {
			return err
		}
	}
	for _, id := range projects {
		files[filepath.Join(dir, sqlite.ProjectsDir, string(id)+".db")] = project
	}
	for file, image := range files {
		if err := os.WriteFile(file, image, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// imageConn is the part of the SQLite driver's connection that reads a database out as bytes and loads
// bytes back in as a database.
type imageConn interface {
	Serialize() ([]byte, error)
	Deserialize([]byte) error
}

// migratedImage migrates a new in-memory database and reads out its bytes.
func migratedImage(migrate func(context.Context, *sql.DB) error) ([]byte, error) {
	ctx := context.Background()
	db, err := sqlite.OpenMemoryDB()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	if err := migrate(ctx, db); err != nil {
		return nil, err
	}
	var image []byte
	err = onConnection(ctx, db, func(c imageConn) (err error) {
		image, err = c.Serialize()
		return err
	})
	return image, err
}

// openMigratedDB opens a new in-memory database that holds its own copy of the migrated ledger.
func openMigratedDB() (*sql.DB, error) {
	image, err := ledgerImage()
	if err != nil {
		return nil, err
	}
	return openImage(image)
}

// openImage opens a new in-memory database that holds its own copy of an image.
func openImage(image []byte) (*sql.DB, error) {
	db, err := sqlite.OpenMemoryDB()
	if err != nil {
		return nil, err
	}
	if err := onConnection(context.Background(), db, func(c imageConn) error { return c.Deserialize(image) }); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// onConnection runs use on the one connection an in-memory database lives on.
func onConnection(ctx context.Context, db *sql.DB, use func(imageConn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return conn.Raw(func(driverConn any) error {
		c, ok := driverConn.(imageConn)
		if !ok {
			return fmt.Errorf("fake: the SQLite driver's connection %T cannot read or load a database as bytes", driverConn)
		}
		return use(c)
	})
}
