//go:build postgres

package pgtest

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/contract"
)

var (
	sharedOnce sync.Once
	shared     *Server
	sharedErr  error
)

// Shared returns the one server of this test binary, starting it on first use.
func Shared() (*Server, error) {
	sharedOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		shared, sharedErr = Start(ctx)
	})
	return shared, sharedErr
}

// StopShared stops the server, if one started. A TestMain calls it.
func StopShared() error {
	if shared == nil {
		return nil
	}
	return shared.Stop()
}

// Backend runs the contract suite on the shared server.
var Backend = contract.Backend{Name: "postgres", Fresh: func(t testing.TB) contract.Database {
	server, err := Shared()
	if err != nil {
		t.Fatal(err)
	}
	name, err := server.NewDatabase(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	offset := &atomic.Int64{}
	return contract.Database{
		Restartable: true,
		ShiftClock:  func(delta int64) { offset.Add(delta) },
		Open: func(t testing.TB, opts sqlstore.Options) ports.Store {
			db, err := sql.Open("pgx", server.DSN(name))
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(16)
			store := sqlstore.New(db, contract.ShiftedDialect{Dialect: Dialect{}, Offset: offset}, opts)
			t.Cleanup(func() { _ = store.Close() })
			return store
		},
		Damage: func(_ testing.TB, statement string) error {
			db, err := sql.Open("pgx", server.DSN(name))
			if err != nil {
				return err
			}
			defer db.Close()
			_, err = db.Exec(statement)
			return err
		},
	}
}}
