// Package apitest serves the real handler over a real unix socket, on an in-memory
// Store, so a test exercises the same path a client takes.
package apitest

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/lexical"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Scope is the workspace of every harness.
const Scope = ledger.WorkspaceID("test-workspace")

// Project is the name a harness's one project answers to, under /v1/projects/{project}/.
const ProjectName = ledger.DefaultProjectName

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
	// Client calls the harness's project as its member, and the installation's routes through the client
	// it carries. Session is what it presents.
	Client  *api.ProjectClient
	Session string
	// Config is what the handler runs on, for a second listener on the same ledger.
	Config server.Config
}

// Web is the web listener of one daemon, on a real loopback port.
type Web struct {
	Host string
	Port int
	HTTP *http.Client
}

// URL is an address on this listener.
func (w *Web) URL(path string) string { return "http://" + w.Host + path }

// ServeWeb serves a daemon's web listener until the test ends.
func (d *Daemon) ServeWeb(t testing.TB, assets fs.FS, recheck time.Duration) *Web {
	t.Helper()
	listener, err := server.ListenWeb("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	handler := server.NewWeb(d.Config, server.WebOptions{Port: port, Assets: assets, Recheck: recheck})
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
	return &Web{Host: listener.Addr().String(), Port: port, HTTP: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
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
func SignIn(ctx context.Context, client signingIn) (string, error) {
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

// signingIn is the part of a client that signs in, which a project's client carries too.
type signingIn interface {
	SetPassphrase(context.Context, api.SetPassphrase) error
	SignIn(context.Context, api.SignIn) (api.Session, error)
	UseSession(string)
}

// RawClient is an HTTP client on a socket, for a test that must send what the API's client never would.
func RawClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		},
	}}
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
	// Windows says how long a message stays useful, per kind.
	Windows messages.Windows
	// Telemetry is the local telemetry store the API reads. Nil has none.
	Telemetry ports.TelemetryReader
	// Backup copies the ledger. Nil has none.
	Backup ports.LedgerBackup
	// Panes is the worker's terminal as the web pane reaches it. Nil reaches none.
	Panes server.Panes
	// WebAddress is where the daemon says its web UI listens. Empty serves none.
	WebAddress string
	// Checkouts is the machine of the daemon's worker. Nil has none, and registering a repository refuses.
	Checkouts ports.Checkouts
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
	ctx := context.Background()
	db, err := openMigrated(ctx)
	if err != nil {
		t.Fatal(err)
	}
	store := sqlstore.New(db, dialect, sqlstore.Options{})
	t.Cleanup(func() { _ = store.Close() })
	actor := ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: Scope, Name: "Test"}, actor); err != nil {
		t.Fatal(err)
	}
	identity := fake.NewIdentityProvider()
	config := server.Config{
		Scope: Scope, Store: store, LeaseTTL: LeaseTTL, Protocol: options.Protocol, HeartbeatMs: HeartbeatMs,
		Knowledge: options.Knowledge, Matcher: lexical.Matcher{Store: store}, Identity: identity, Windows: options.Windows,
		Telemetry: options.Telemetry, Backup: options.Backup, Panes: options.Panes, WebAddress: options.WebAddress,
		Checkouts: options.Checkouts,
	}
	daemon := serve(t, config, options.Wrap)
	daemon.Store = store
	if !options.NoMember {
		daemon.signIn(t, identity)
	}
	return daemon
}

// serve serves a handler on its own socket until the test ends.
func serve(t testing.TB, config server.Config, wrap func(http.Handler) http.Handler) *Daemon {
	t.Helper()
	socket := filepath.Join(ShortDir(t), api.ControlSocketName)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.New(config)
	if wrap != nil {
		handler = wrap(handler)
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
	return &Daemon{Socket: socket, Client: client.Project(ProjectName), Config: config}
}

// Installation is an installation on a state directory: its first project `default` on the ledger file,
// and every other project in a database of its own, served by one API with one member, with the routes
// with no project kept for one release, as the daemon serves them.
type Installation struct {
	*Daemon
	State *sqlite.State
	// Checkouts is the machine of the worker the daemon runs, with the clones a test places on it.
	Checkouts *fake.Checkouts
}

// StartInstallation serves an installation with the projects named after its first one, until the test
// ends. Each project's ID is its name with "-id" after it, so a test can tell the two apart.
func StartInstallation(t testing.TB, names ...string) *Installation {
	t.Helper()
	ctx := context.Background()
	actor := ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}
	dir := t.TempDir()
	ids := make([]ledger.WorkspaceID, len(names))
	for i, name := range names {
		ids[i] = ledger.WorkspaceID(name + "-id")
	}
	if err := seedState(ctx, dir, ids...); err != nil {
		t.Fatal(err)
	}
	state, store, err := sqlite.Upgrade(ctx, dir, api.LedgerFileName, ledger.Workspace{ID: Scope, Name: "Test"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = state.Close()
		_ = store.Close()
	})
	for i, name := range names {
		if _, _, err := state.CreateProject(ctx, ledger.Project{ID: ids[i], Name: name}, actor); err != nil {
			t.Fatal(err)
		}
	}
	identity := fake.NewIdentityProvider()
	machine := fake.NewCheckouts()
	config := server.Config{
		Scope: Scope, Store: store, Projects: state, Installation: state.Installation, LeaseTTL: LeaseTTL, HeartbeatMs: HeartbeatMs, Identity: identity,
		Matcher:   lexical.Matcher{Store: store},
		Matching:  func(store ports.Store) ports.Matcher { return lexical.Matcher{Store: store} },
		Checkouts: machine,
	}
	daemon := serve(t, config, nil)
	daemon.Store = store
	daemon.signIn(t, identity)
	return &Installation{Daemon: daemon, State: state, Checkouts: machine}
}

// In returns the Store and the member's client of one project.
func (in *Installation) In(t testing.TB, name string) (ledger.Project, ports.Store, *api.ProjectClient) {
	t.Helper()
	project, err := in.State.Named(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	store, err := in.State.Open(t.Context(), project)
	if err != nil {
		t.Fatal(err)
	}
	return project, store, in.Client.Project(name)
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

// Route is where a project route, as it reads under /v1, lives for the harness's project.
func Route(path string) string { return api.InProject(ProjectName, path) }
