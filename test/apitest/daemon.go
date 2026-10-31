// Package apitest serves the real handler over a real unix socket, on an in-memory
// Store, so a test exercises the same path a client takes.
package apitest

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/api/server"
	"ropeshift.dev/ropeshift/internal/ledger"
)

// Scope is the workspace of every harness.
const Scope = ledger.WorkspaceID("test-workspace")

// LeaseTTL is the policy lease of every harness.
const LeaseTTL = int64(60_000)

// HeartbeatMs is the heartbeat of every harness.
const HeartbeatMs = int64(15_000)

// Daemon is one served API.
type Daemon struct {
	Socket string
	Store  *sqlstore.Store
	Client *api.Client
}

// maxShortBase keeps a socket under the 104-byte limit with a directory, a run
// directory, and a session's private directory on top. A macOS temporary directory
// is longer than this.
const maxShortBase = 20

// ShortDir makes a directory whose path leaves room for a socket.
func ShortDir(t testing.TB) string {
	t.Helper()
	base := os.TempDir()
	if len(base) > maxShortBase {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "rs")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(resolved) })
	return resolved
}

// Options changes what a harness serves.
type Options struct {
	// Protocol is the control plane's protocol version, zero for this build's.
	Protocol int
	// Wrap stands between the socket and the handler, which is how a test breaks a
	// response on the wire.
	Wrap func(http.Handler) http.Handler
	// Clock wraps the dialect, which is how a test moves database time.
	Clock func(sqlstore.Dialect) sqlstore.Dialect
}

// Start serves the API until the test ends.
func Start(t testing.TB) *Daemon {
	t.Helper()
	return StartWith(t, Options{})
}

// StartWith serves the API with options until the test ends.
func StartWith(t testing.TB, options Options) *Daemon {
	t.Helper()
	var dialect sqlstore.Dialect = sqlite.Dialect{}
	if options.Clock != nil {
		dialect = options.Clock(dialect)
	}
	db, err := sqlite.OpenMemoryDB()
	if err != nil {
		t.Fatal(err)
	}
	store := sqlstore.New(db, dialect, sqlstore.Options{})
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	actor := ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: Scope, Name: "Test"}, actor); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(ShortDir(t), api.ControlSocketName)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.New(server.Config{
		Scope: Scope, Store: store, LeaseTTL: LeaseTTL, Protocol: options.Protocol, HeartbeatMs: HeartbeatMs,
	})
	if options.Wrap != nil {
		handler = options.Wrap(handler)
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = httpServer.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = httpServer.Close()
		<-done
	})
	client := api.NewUnixClient(socket)
	t.Cleanup(client.Close)
	return &Daemon{Socket: socket, Store: store, Client: client}
}
