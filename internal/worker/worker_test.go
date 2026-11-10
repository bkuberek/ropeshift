package worker_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/lexical"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

const scope = ledger.WorkspaceID("w")

var member = ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}

type rig struct {
	t       *testing.T
	store   *fake.Store
	plane   *control.Workers
	link    *fake.WorkerLink
	journal *fake.Journal
	clock   *fake.Clock
	// deadline is the next work order's, in Unix milliseconds.
	deadline int64
	// workerJournal stands between a lane rig's worker and the journal when a test must
	// stop that worker at one call. Nil leaves the journal itself.
	workerJournal ports.Journal
	// offset moves database time.
	offset *atomic.Int64
	// kind is the next work order's, and a change when it is empty.
	kind workorders.Kind
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := t.Context()
	offset := &atomic.Int64{}
	image, err := migratedStore()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.OpenMemoryDB()
	if err != nil {
		t.Fatal(err)
	}
	if err := onConnection(ctx, db, func(c imageConn) error { return c.Deserialize(image) }); err != nil {
		t.Fatal(err)
	}
	store := &fake.Store{Store: sqlstore.New(db, shiftedDialect{Dialect: sqlite.Dialect{}, offset: offset}, sqlstore.Options{})}
	t.Cleanup(func() { _ = store.Close() })
	// Every migration finds itself applied, which is the path a restarted store takes.
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: scope}, member); err != nil {
		t.Fatal(err)
	}
	journal, err := fake.NewJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	plane := control.NewWorkers(store, scope, control.WorkerOptions{HeartbeatMs: 20, LeaseTTLMs: 60_000, Matcher: lexical.Matcher{Store: store}})
	return &rig{
		t: t, store: store, plane: plane, link: &fake.WorkerLink{Plane: plane},
		journal: journal, clock: fake.NewClock(time.UnixMilli(1_000_000)), offset: offset,
	}
}

// migratedStore is the image of a store every migration has run on, made once per test
// binary. Each rig starts from a copy of it: under -race the migrations cost far more than
// most tests, and every rig ran them again.
var migratedStore = sync.OnceValues(func() ([]byte, error) {
	ctx := context.Background()
	db, err := sqlite.OpenMemoryDB()
	if err != nil {
		return nil, err
	}
	store := sqlstore.New(db, shiftedDialect{Dialect: sqlite.Dialect{}, offset: &atomic.Int64{}}, sqlstore.Options{})
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

// imageConn is the SQLite driver's connection, which reads and writes a whole database image.
type imageConn interface {
	Serialize() ([]byte, error)
	Deserialize([]byte) error
}

// onConnection runs a function on the one connection a memory database lives on.
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

// shiftedDialect adds an offset to database time, which is how a test moves that clock.
type shiftedDialect struct {
	sqlstore.Dialect
	offset *atomic.Int64
}

func (d shiftedDialect) ClockSQL() string {
	inner := strings.TrimPrefix(d.Dialect.ClockSQL(), "SELECT ")
	return "SELECT (" + inner + ") + " + strconv.FormatInt(d.offset.Load(), 10)
}

// shiftDatabase moves database time, which the worker's own clock has no part in.
func (r *rig) shiftDatabase(t *testing.T, deltaMs int64) {
	t.Helper()
	r.offset.Add(deltaMs)
}

func (r *rig) subject() *worker.Worker {
	return worker.New(r.link, r.journal, r.clock, worker.Config{
		Name: "test",
		JoinToken: func(ctx context.Context) (string, error) {
			return r.plane.CreateJoinToken(ctx, member)
		},
	})
}

func (r *rig) started() *worker.Worker {
	r.t.Helper()
	subject := r.subject()
	if err := subject.Start(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	return subject
}

func (r *rig) claimFor(id, worker string) workorders.Grant {
	r.t.Helper()
	ctx := r.t.Context()
	kind := r.kind
	if kind == "" {
		kind = workorders.KindChange
	}
	_, err := control.NewWorkOrders(r.store).Record(ctx, scope, control.LedgerWork{
		Order:    workorders.WorkOrder{ID: id, Kind: kind, Title: "T", Deadline: r.deadline},
		Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}, member)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := control.NewReviews(r.store).Pass(ctx, scope, id, member); err != nil {
		r.t.Fatal(err)
	}
	grant, err := control.NewLeases(r.store).Claim(ctx, scope, workorders.ClaimRequest{
		WorkOrder: id, ClaimID: "claim-" + id, Worker: worker, TTLMs: 60_000,
	}, member)
	if err != nil {
		r.t.Fatal(err)
	}
	return grant
}

func (r *rig) events() []ledger.Event {
	r.t.Helper()
	var events []ledger.Event
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		events, err = control.ReadAllEvents(tx.Events)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return events
}

func TestFirstStartRegistersOnce(t *testing.T) {
	r := newRig(t)
	first := r.started()
	id := first.Auth().WorkerID
	if id == "" || first.Auth().Credential == "" {
		t.Fatalf("auth %+v", first.Auth())
	}
	calls := 0
	again := worker.New(r.link, r.journal, r.clock, worker.Config{
		JoinToken: func(context.Context) (string, error) {
			calls++
			return "", errors.New("a registered worker needs no token")
		},
	})
	if err := again.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || again.Auth().WorkerID != id {
		t.Errorf("%d calls, id %s after %s", calls, again.Auth().WorkerID, id)
	}
	if again.Session().Epoch != first.Session().Epoch+1 {
		t.Errorf("epochs %d then %d", first.Session().Epoch, again.Session().Epoch)
	}
}

func TestRegistrationResumesFromTheJournal(t *testing.T) {
	r := newRig(t)
	if err := r.journal.SaveIdentity(t.Context(), ledger.WorkerIdentity{WorkerID: "kept"}); err != nil {
		t.Fatal(err)
	}
	if got := r.started().Auth().WorkerID; got != "kept" {
		t.Errorf("the worker took a new ID: %s", got)
	}
}

func TestStartNeedsAToken(t *testing.T) {
	r := newRig(t)
	subject := worker.New(r.link, r.journal, r.clock, worker.Config{})
	if err := subject.Start(t.Context()); err == nil {
		t.Error("a worker with no token and no identity started")
	}
}

func TestDesiredStateReachesTheJournal(t *testing.T) {
	r := newRig(t)
	subject := r.started()
	grant := r.claimFor("wo", subject.Auth().WorkerID)
	if _, err := subject.Beat(t.Context()); err != nil {
		t.Fatal(err)
	}
	leases, err := r.journal.Leases(t.Context())
	if err != nil || len(leases) != 1 || leases[0].Fence != grant.Fence {
		t.Fatalf("leases %+v: %v", leases, err)
	}
	state, err := r.journal.Desired(t.Context())
	if err != nil || len(state.Leases) != 1 {
		t.Errorf("cached %+v: %v", state, err)
	}
}

func TestLeaseDeadlineCountsFromTheSend(t *testing.T) {
	r := newRig(t)
	subject := r.started()
	r.claimFor("wo", subject.Auth().WorkerID)
	if _, err := subject.Beat(t.Context()); err != nil {
		t.Fatal(err)
	}
	sent := r.clock.Now()
	slow := &slowLink{WorkerLink: r.link, clock: r.clock, delay: 5 * time.Second}
	late := worker.New(slow, r.journal, r.clock, worker.Config{})
	if err := late.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := late.Beat(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline, ok := late.Deadline("wo")
	if !ok {
		t.Fatal("no deadline")
	}
	if want := sent.Add(60 * time.Second); !deadline.Equal(want) {
		t.Errorf("deadline %v, want %v from the send", deadline, want)
	}
}

type slowLink struct {
	*fake.WorkerLink
	clock *fake.Clock
	delay time.Duration
}

func (l *slowLink) Heartbeat(ctx context.Context, beat ledger.Heartbeat) (ledger.HeartbeatReply, error) {
	reply, err := l.WorkerLink.Heartbeat(ctx, beat)
	l.clock.Advance(l.delay)
	return reply, err
}

func TestFencedLeaseLeavesTheJournal(t *testing.T) {
	r := newRig(t)
	subject := r.started()
	grant := r.claimFor("wo", subject.Auth().WorkerID)
	ctx := t.Context()
	if _, err := subject.Beat(ctx); err != nil {
		t.Fatal(err)
	}
	if err := control.NewLeases(r.store).Release(ctx, scope, grant.Lease, member); err != nil {
		t.Fatal(err)
	}
	reply, err := subject.Beat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Fenced) != 1 {
		t.Fatalf("reply %+v", reply)
	}
	leases, err := r.journal.Leases(ctx)
	if err != nil || len(leases) != 0 {
		t.Errorf("leases %+v: %v", leases, err)
	}
	if _, ok := subject.Deadline("wo"); ok {
		t.Error("a fenced lease kept its deadline")
	}
}

func TestWrongNonceChangesNothing(t *testing.T) {
	r := newRig(t)
	subject := r.started()
	r.claimFor("wo", subject.Auth().WorkerID)
	ctx := t.Context()
	if _, err := subject.Beat(ctx); err != nil {
		t.Fatal(err)
	}
	r.link.CorruptNonce.Store(true)
	r.clock.Advance(time.Minute)
	if _, err := subject.Beat(ctx); !errors.Is(err, ledger.ErrNonce) {
		t.Fatalf("got %v", err)
	}
	deadline, _ := subject.Deadline("wo")
	if deadline.After(r.clock.Now()) {
		t.Errorf("an unproven answer extended the deadline to %v", deadline)
	}
}

func TestLostUploadAnswerStoresOneEvent(t *testing.T) {
	r := newRig(t)
	subject := r.started()
	grant := r.claimFor("wo", subject.Auth().WorkerID)
	ctx := t.Context()
	recorded, err := subject.Record(ctx, ledger.UploadEvent{
		Type: "note", WorkOrder: "wo", ClaimID: grant.ClaimID, Fence: grant.Fence, Payload: []byte("once"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Epoch != subject.Session().Epoch || recorded.OccurredAt != r.clock.Now().UnixMilli() {
		t.Errorf("recorded %+v", recorded)
	}
	r.link.LoseUploadAnswer.Store(true)
	if err := subject.Upload(ctx); !errors.Is(err, fake.ErrAnswerLost) {
		t.Fatalf("got %v", err)
	}
	if err := subject.Upload(ctx); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range r.events() {
		if e.Type == "work_order.note" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("%d events stored", count)
	}
	pending, err := r.journal.Pending(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Errorf("%d pending: %v", len(pending), err)
	}
}

func TestFencedEventIsKept(t *testing.T) {
	r := newRig(t)
	subject := r.started()
	grant := r.claimFor("wo", subject.Auth().WorkerID)
	ctx := t.Context()
	if err := control.NewLeases(r.store).Release(ctx, scope, grant.Lease, member); err != nil {
		t.Fatal(err)
	}
	if _, err := subject.Record(ctx, ledger.UploadEvent{Type: "note", WorkOrder: "wo", ClaimID: grant.ClaimID, Fence: grant.Fence}); err != nil {
		t.Fatal(err)
	}
	if err := subject.Upload(ctx); err != nil {
		t.Fatal(err)
	}
	rejected, err := r.journal.Rejected(ctx)
	if err != nil || len(rejected) != 1 || rejected[0].Status != ledger.UploadFenced {
		t.Errorf("rejected %+v: %v", rejected, err)
	}
}

func TestEventsWaitForAnAbsentControlPlane(t *testing.T) {
	r := newRig(t)
	subject := r.started()
	ctx := t.Context()
	down := &downLink{WorkerLink: r.link}
	offline := worker.New(down, r.journal, r.clock, worker.Config{})
	down.down.Store(true)
	if err := offline.Start(ctx); !errors.Is(err, errDown) {
		t.Fatalf("got %v", err)
	}
	if _, err := subject.Record(ctx, ledger.UploadEvent{Type: "note"}); err != nil {
		t.Fatal(err)
	}
	pending, err := r.journal.Pending(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Errorf("%d pending: %v", len(pending), err)
	}
}

var errDown = errors.New("the control plane is down")

type downLink struct {
	*fake.WorkerLink
	down atomic.Bool
}

func (l *downLink) OpenSession(ctx context.Context, request ledger.SessionRequest) (ledger.Session, error) {
	if l.down.Load() {
		return ledger.Session{}, errDown
	}
	return l.WorkerLink.OpenSession(ctx, request)
}

func TestRunConvergesAndStops(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	subject := r.subject()
	done := make(chan error, 1)
	go func() { done <- subject.Run(ctx) }()
	deadline := time.Now().Add(30 * time.Second)
	for subject.Auth().WorkerID == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	grant := r.claimFor("wo", subject.Auth().WorkerID)
	for time.Now().Before(deadline) {
		leases, err := r.journal.Leases(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(leases) == 1 && leases[0].Fence == grant.Fence {
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Errorf("run ended with %v", err)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the worker never converged")
}

func TestRunStopsOnARefusedProtocol(t *testing.T) {
	r := newRig(t)
	subject := worker.New(r.link, r.journal, r.clock, worker.Config{
		Protocol: 99,
		JoinToken: func(ctx context.Context) (string, error) {
			return r.plane.CreateJoinToken(ctx, member)
		},
	})
	var refused *ledger.ProtocolError
	if err := subject.Run(t.Context()); !errors.As(err, &refused) {
		t.Errorf("got %v", err)
	}
}

func TestNewEpochAfterAnotherProcessReplacedThisOne(t *testing.T) {
	r := newRig(t)
	first := r.started()
	r.started()
	if _, err := first.Beat(t.Context()); !errors.Is(err, ledger.ErrStaleEpoch) {
		t.Errorf("got %v", err)
	}
}
