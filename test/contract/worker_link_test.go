package contract

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/adapters/workerlink"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

func memoryLink(t *testing.T, protocol int) *LinkRig {
	t.Helper()
	offset := &atomic.Int64{}
	db, err := sqlite.OpenMemoryDB()
	if err != nil {
		t.Fatal(err)
	}
	store := sqlstore.New(db, ShiftedDialect{Dialect: sqlite.Dialect{}, Offset: offset}, sqlstore.Options{})
	t.Cleanup(func() { _ = store.Close() })
	if err := restoreMigrated(db); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	// Every migration finds itself applied, which is the path a restarted store takes.
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	scope := ledger.WorkspaceID("w")
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: scope}, ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}); err != nil {
		t.Fatal(err)
	}
	plane := control.NewWorkers(store, scope, control.WorkerOptions{Protocol: protocol, HeartbeatMs: linkHeartbeat, LeaseTTLMs: linkTTL})
	link := &fake.WorkerLink{Plane: plane}
	return &LinkRig{
		Store: store, Scope: scope, Plane: plane, Link: link,
		CorruptNonce: func() { link.CorruptNonce.Store(true) },
		DropHints:    func() { link.DropHints.Store(true) },
		ShiftClock:   func(delta int64) { offset.Add(delta) },
	}
}

// projectLink is memoryLink on a project of an installation, whose workers live in the installation
// database (ADR-0021).
func projectLink(t *testing.T, protocol int) *LinkRig {
	t.Helper()
	database := SQLiteProject.Fresh(t)
	store := database.Open(t, sqlstore.Options{})
	ctx := t.Context()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	scope := ledger.WorkspaceID("w")
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: scope}, ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}); err != nil {
		t.Fatal(err)
	}
	plane := control.NewWorkers(store, scope, control.WorkerOptions{Protocol: protocol, HeartbeatMs: linkHeartbeat, LeaseTTLMs: linkTTL})
	link := &fake.WorkerLink{Plane: plane}
	return &LinkRig{
		Store: store, Scope: scope, Plane: plane, Link: link,
		CorruptNonce: func() { link.CorruptNonce.Store(true) },
		DropHints:    func() { link.DropHints.Store(true) },
		ShiftClock:   database.ShiftClock,
	}
}

// faultyWire stands between the socket and the handler, so a fault lands on the
// bytes a real client reads.
type faultyWire struct {
	next         http.Handler
	corruptNonce atomic.Bool
	dropHints    atomic.Bool
}

func (f *faultyWire) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case f.dropHints.Load() && strings.HasSuffix(r.URL.Path, "/watch"):
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	case f.corruptNonce.Load() && strings.HasSuffix(r.URL.Path, "/heartbeat"):
		recorded := httptest.NewRecorder()
		f.next.ServeHTTP(recorded, r)
		var body map[string]any
		if err := json.Unmarshal(recorded.Body.Bytes(), &body); err != nil {
			panic(err)
		}
		body["nonce"] = "someone-else"
		_ = json.NewEncoder(w).Encode(body)
	default:
		f.next.ServeHTTP(w, r)
	}
}

func httpLink(t *testing.T, protocol int) *LinkRig {
	t.Helper()
	wire := &faultyWire{}
	offset := &atomic.Int64{}
	daemon := apitest.StartWith(t, apitest.Options{
		Protocol: protocol,
		Clock:    func(d sqlstore.Dialect) sqlstore.Dialect { return ShiftedDialect{Dialect: d, Offset: offset} },
		Wrap: func(next http.Handler) http.Handler {
			wire.next = next
			return wire
		},
	})
	plane := control.NewWorkers(daemon.Store, apitest.Scope, control.WorkerOptions{Protocol: protocol, HeartbeatMs: linkHeartbeat, LeaseTTLMs: linkTTL})
	return &LinkRig{
		Store: daemon.Store, Scope: apitest.Scope, Plane: plane, Link: workerlink.New(daemon.Client.Client),
		CorruptNonce: func() { wire.corruptNonce.Store(true) },
		DropHints:    func() { wire.dropHints.Store(true) },
		ShiftClock:   func(delta int64) { offset.Add(delta) },
	}
}

// tcpLink is httpLink on a TCP listener: the group server's network listener, whose every route carries a
// credential, reached by the client a group worker uses over a loopback address.
func tcpLink(t *testing.T, protocol int) *LinkRig {
	t.Helper()
	return networkLink(t, protocol, nil, func(network *apitest.Web) (*api.Client, error) {
		return api.NewTCPClient("http://" + network.Host)
	})
}

// networkLink is the rig of every link on the network listener. serving is what the listener serves, nil for
// plaintext, and dial makes the client a worker would use on the address the listener bound. with changes how
// the listener is served, as a link behind a proxy lists the proxy.
func networkLink(t *testing.T, protocol int, serving *tls.Config, dial func(*apitest.Web) (*api.Client, error), with ...func(*apitest.NetworkOptions)) *LinkRig {
	t.Helper()
	wire := &faultyWire{}
	offset := &atomic.Int64{}
	daemon := apitest.StartWith(t, apitest.Options{
		Protocol: protocol,
		Clock:    func(d sqlstore.Dialect) sqlstore.Dialect { return ShiftedDialect{Dialect: d, Offset: offset} },
	})
	options := apitest.NetworkOptions{
		Assets: fstest.MapFS{"placeholder.html": {Data: []byte("not built")}}, TLS: serving,
		Wrap: func(next http.Handler) http.Handler {
			wire.next = next
			return wire
		},
	}
	for _, change := range with {
		change(&options)
	}
	network := daemon.ServeNetworkWith(t, options)
	client, err := dial(network)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	plane := control.NewWorkers(daemon.Store, apitest.Scope, control.WorkerOptions{Protocol: protocol, HeartbeatMs: linkHeartbeat, LeaseTTLMs: linkTTL})
	return &LinkRig{
		Store: daemon.Store, Scope: apitest.Scope, Plane: plane, Link: workerlink.New(client),
		CorruptNonce: func() { wire.corruptNonce.Store(true) },
		DropHints:    func() { wire.dropHints.Store(true) },
		ShiftClock:   func(delta int64) { offset.Add(delta) },
	}
}

// TestWorkerLinkContract runs with no other test, and its backends one after the other, so
// the check that reads a window of real time runs alone.
func TestWorkerLinkContract(t *testing.T) {
	t.Run("in-memory", func(t *testing.T) {
		RunWorkerLink(t, LinkBackend{Name: "in-memory", Fresh: memoryLink})
	})
	t.Run("http", func(t *testing.T) {
		RunWorkerLink(t, LinkBackend{Name: "http", Fresh: httpLink})
	})
	t.Run("project", func(t *testing.T) {
		RunWorkerLink(t, LinkBackend{Name: "project", Fresh: projectLink})
	})
	t.Run("tcp", func(t *testing.T) {
		RunWorkerLink(t, LinkBackend{Name: "tcp", Fresh: tcpLink})
	})
	t.Run("https with the server's own authority", func(t *testing.T) {
		RunWorkerLink(t, LinkBackend{Name: "https-own-authority", Fresh: secureLink(ownAuthority)})
	})
	t.Run("https with a supplied certificate", func(t *testing.T) {
		RunWorkerLink(t, LinkBackend{Name: "https-supplied-certificate", Fresh: secureLink(suppliedCertificate)})
	})
	t.Run("https behind a reverse proxy", func(t *testing.T) {
		RunWorkerLink(t, LinkBackend{Name: "https-behind-a-proxy", Fresh: proxiedLink})
	})
}

// TestCredentialStaysOutOfTheLedger holds NFR-SEC-4: the event log and the worker
// table keep no credential or join token.
func TestCredentialStaysOutOfTheLedger(t *testing.T) {
	t.Parallel()
	r := memoryLink(t, 0)
	ctx := t.Context()
	auth := r.register(t, ctx, "a")
	var events []ledger.Event
	var workers []ledger.Worker
	err := r.Store.Read(ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		if events, err = control.ReadAllEvents(tx.Events); err != nil {
			return err
		}
		workers, err = tx.Workers().List()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal([]any{events, workers})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(auth.Credential)) {
		t.Error("a credential reached the ledger")
	}
}

var _ = api.CodeFenced
