package apitest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

// Applying the schema is the dearest step of starting a harness: each migration is a transaction of DDL,
// and SQLite parses the whole schema again after every one. The schema is the same for every harness, so
// a process migrates a database the first time a harness asks and keeps its bytes. Each harness gets a
// copy that no other harness reaches: loaded into an in-memory database, or written as the file a state
// directory holds. The bytes are read-only once made. A test of the migrations themselves never starts a
// harness, and the migrations a copy records carry the time the process made the template.

// imaging is the part of the SQLite driver's connection that reads a database out as bytes and loads
// bytes back in as a database.
type imaging interface {
	Serialize() ([]byte, error)
	Deserialize([]byte) error
}

// template is one migrated database, made once.
type template struct {
	once  sync.Once
	image []byte
	err   error
	// migrate brings a new in-memory database to this build's schema.
	migrate func(context.Context, *sql.DB) error
}

// ledgerTemplate is a migrated ledger, which a project's database and a daemon's ledger file also are.
var ledgerTemplate = &template{migrate: func(ctx context.Context, db *sql.DB) error {
	return sqlstore.New(db, sqlite.Dialect{}, sqlstore.Options{}).Migrate(ctx)
}}

// installationTemplate is a migrated installation database.
var installationTemplate = &template{migrate: func(ctx context.Context, db *sql.DB) error {
	return sqlstore.NewInstallation(db, sqlite.Dialect{}, sqlstore.Options{}).Migrate(ctx)
}}

// bytes migrates the template's database the first time and returns what it holds.
func (m *template) bytes(ctx context.Context) ([]byte, error) {
	m.once.Do(func() { m.image, m.err = m.build(ctx) })
	return m.image, m.err
}

// build migrates a new in-memory database and reads out its bytes.
func (m *template) build(ctx context.Context) ([]byte, error) {
	db, err := sqlite.OpenMemoryDB()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	if err := m.migrate(ctx, db); err != nil {
		return nil, err
	}
	var image []byte
	err = onDriverConn(ctx, db, func(c imaging) (err error) {
		image, err = c.Serialize()
		return err
	})
	return image, err
}

// openMigrated opens an in-memory ledger that holds the schema this build migrates to, with no row of
// its own but the migrations it records.
func openMigrated(ctx context.Context) (*sql.DB, error) {
	image, err := ledgerTemplate.bytes(ctx)
	if err != nil {
		return nil, err
	}
	db, err := sqlite.OpenMemoryDB()
	if err != nil {
		return nil, err
	}
	if err := onDriverConn(ctx, db, func(c imaging) error { return c.Deserialize(image) }); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// onDriverConn runs use on the one connection an in-memory database lives on.
func onDriverConn(ctx context.Context, db *sql.DB, use func(imaging) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return conn.Raw(func(driverConn any) error {
		c, ok := driverConn.(imaging)
		if !ok {
			return fmt.Errorf("apitest: the SQLite driver's connection %T cannot read or load a database as bytes", driverConn)
		}
		return use(c)
	})
}

// seedState writes a state directory's migrated databases before sqlite.Upgrade opens it: the
// installation database, the first project's ledger, and the ledger of each project named, which
// State.CreateProject puts in the projects folder under the project's ID. Upgrade and CreateProject
// still run whole, and find every migration applied. SQLite opens each file in the rollback journal it
// was written in and puts it in WAL mode, as it does a file it has just made.
func seedState(ctx context.Context, dir string, projects ...ledger.WorkspaceID) error {
	ledgerImage, err := ledgerTemplate.bytes(ctx)
	if err != nil {
		return err
	}
	installationImage, err := installationTemplate.bytes(ctx)
	if err != nil {
		return err
	}
	files := map[string][]byte{
		filepath.Join(dir, sqlite.InstallationFileName): installationImage,
		filepath.Join(dir, api.LedgerFileName):          ledgerImage,
	}
	if len(projects) > 0 {
		if err := os.MkdirAll(filepath.Join(dir, sqlite.ProjectsDir), 0o700); err != nil {
			return err
		}
	}
	for _, id := range projects {
		files[filepath.Join(dir, sqlite.ProjectsDir, string(id)+".db")] = ledgerImage
	}
	for file, image := range files {
		if err := os.WriteFile(file, image, 0o644); err != nil {
			return err
		}
	}
	return nil
}
