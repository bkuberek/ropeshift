// Package apitest serves the real handler over a real unix socket, on an in-memory
// Store, so a test exercises the same path a client takes.
package apitest

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/lexical"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/api/server"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// Scope is the workspace of every harness.
const Scope = ledger.WorkspaceID("test-workspace")

// LeaseTTL is the policy lease of every harness.
const LeaseTTL = int64(60_000)

// HeartbeatMs is the heartbeat of every harness.
const HeartbeatMs = int64(15_000)

// Passphrase is the passphrase of every harness.
const Passphrase = "a passphrase only a person knows"

// Daemon is one served API.
type Daemon struct {
	Socket string
	Store  *sqlstore.Store
	// Client calls as the harness's member, and Session is what it presents.
	Client  *api.Client
	Session string
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

// SignIn gives a client a session on a server this harness did not start, over the API,
// which is the path a person's own client takes. It sets the harness's passphrase if nobody
// has, and it returns the token for a process the client cannot hand it to.
func SignIn(ctx context.Context, client *api.Client) (string, error) {
	err := client.SetPassphrase(ctx, api.SetPassphrase{Passphrase: Passphrase})
	var refused *api.Error
	if err != nil && (!errors.As(err, &refused) || refused.Detail.Code != api.CodeSessionRequired) {
		return "", err
	}
	session, err := client.SignIn(ctx, api.SignIn{Passphrase: Passphrase})
	if err != nil {
		return "", err
	}
	client.UseSession(session.Token)
	return session.Token, nil
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
	// Knowledge is what the API reads documents from.
	Knowledge []ports.KnowledgeSource
	// NoMember leaves the daemon with no passphrase and the client with no session.
	NoMember bool
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
	identity := fake.NewIdentityProvider()
	handler := server.New(server.Config{
		Scope: Scope, Store: store, LeaseTTL: LeaseTTL, Protocol: options.Protocol, HeartbeatMs: HeartbeatMs,
		Knowledge: options.Knowledge, Matcher: lexical.Matcher{Store: store}, Identity: identity,
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
	daemon := &Daemon{Socket: socket, Store: store, Client: client}
	if !options.NoMember {
		daemon.signIn(t, identity)
	}
	return daemon
}

// signIn enrolls the harness's member and gives the client a session, in process, so that a
// Wrap that breaks the wire cannot break the harness.
func (d *Daemon) signIn(t testing.TB, identity ports.IdentityProvider) {
	t.Helper()
	sessions := control.NewSessions(d.Store, identity)
	ctx := context.Background()
	if err := sessions.Enroll(ctx, Scope, ports.Proof{Passphrase: Passphrase}, ""); err != nil {
		t.Fatal(err)
	}
	grant, err := sessions.SignIn(ctx, Scope, ports.Proof{Passphrase: Passphrase}, 0)
	if err != nil {
		t.Fatal(err)
	}
	d.Session = grant.Token
	d.Client.UseSession(grant.Token)
}

// Completed walks a new work order to `acceptance`, as a lane ending with result leaves it.
func (d *Daemon) Completed(t *testing.T, id string, result workorders.LaneResult) {
	t.Helper()
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	work := control.LedgerWork{
		Order:     workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: "Add the retry budget", MaxAttempts: 1},
		Statement: "Add the retry budget.",
		Criteria:  []planning.Criterion{{ID: "c1", Text: "It retries"}},
	}
	ctx := t.Context()
	if _, err := control.NewWorkOrders(d.Store).Record(ctx, Scope, work, member); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewReviews(d.Store).Pass(ctx, Scope, id, member); err != nil {
		t.Fatal(err)
	}
	leases := control.NewLeases(d.Store)
	worker := ledger.Actor{Kind: ledger.ActorWorker, ID: "worker-a"}
	grant, err := leases.Claim(ctx, Scope, workorders.ClaimRequest{WorkOrder: id, ClaimID: "claim-" + id, Worker: worker.ID, TTLMs: 60_000}, worker)
	if err != nil {
		t.Fatal(err)
	}
	lane := "lane-" + id
	generation, err := leases.StartLane(ctx, Scope, grant.Lease, lane, worker)
	if err != nil {
		t.Fatal(err)
	}
	holder := workorders.Holder{Lease: grant.Lease, Lane: lane, Generation: generation}
	if err := leases.EndLane(ctx, Scope, holder, result, worker); err != nil {
		t.Fatal(err)
	}
}
