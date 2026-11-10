package contract

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

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
	store := sqlstore.New(db, ShiftedDialect{sqlite.Dialect{}, offset}, sqlstore.Options{})
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
		Clock:    func(d sqlstore.Dialect) sqlstore.Dialect { return ShiftedDialect{d, offset} },
		Wrap: func(next http.Handler) http.Handler {
			wire.next = next
			return wire
		},
	})
	plane := control.NewWorkers(daemon.Store, apitest.Scope, control.WorkerOptions{Protocol: protocol, HeartbeatMs: linkHeartbeat, LeaseTTLMs: linkTTL})
	return &LinkRig{
		Store: daemon.Store, Scope: apitest.Scope, Plane: plane, Link: workerlink.New(daemon.Client),
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
