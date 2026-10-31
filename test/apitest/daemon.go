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

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/api/server"
	"ropeshift.dev/ropeshift/internal/ledger"
)

// Scope is the workspace of every harness.
const Scope = ledger.WorkspaceID("test-workspace")

// LeaseTTL is the policy lease of every harness.
const LeaseTTL = int64(60_000)

// Daemon is one served API.
type Daemon struct {
	Socket string
	Store  *fake.Store
	Client *api.Client
}

// ShortDir makes a directory whose path leaves room for a socket.
func ShortDir(t testing.TB) string {
	t.Helper()
	base := os.TempDir()
	if len(base) > 60 {
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

// Start serves the API until the test ends.
func Start(t testing.TB) *Daemon {
	t.Helper()
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
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
	httpServer := &http.Server{
		Handler:           server.New(server.Config{Scope: Scope, Store: store, LeaseTTL: LeaseTTL}),
		ReadHeaderTimeout: 10 * time.Second,
	}
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
