// Package contract is the Store contract suite. One suite runs unchanged on every
// backend (NFR-TEST-2), and a check returns a typed Violation, so that the chaos
// suite can run the same checks on broken variants and tell a caught defect from
// an unrelated error.
package contract

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// Violation is an invariant that did not hold. Any other error from a check is a
// harness failure, so a broken variant cannot pass as caught through a typo.
type Violation struct{ Message string }

func (v *Violation) Error() string { return v.Message }

// Violatedf returns a Violation.
func Violatedf(format string, args ...any) error {
	return &Violation{Message: fmt.Sprintf(format, args...)}
}

// Database is one empty database on a backend.
type Database struct {
	// Open returns a handle on this database. A restart closes one handle and
	// opens another.
	Open func(t testing.TB, opts sqlstore.Options) ports.Store
	// ShiftClock moves database time for every handle.
	ShiftClock func(deltaMs int64)
	// Damage runs one statement around the Store, to corrupt a projection.
	Damage func(t testing.TB, statement string) error
	// Restartable is false for a database that lives inside its only handle.
	Restartable bool
}

// Backend makes empty databases.
type Backend struct {
	Name  string
	Fresh func(t testing.TB) Database
}

// Check is one invariant.
type Check struct {
	ID   string
	Name string
	Run  func(*Rig) error
}

// Rig is one check's world.
type Rig struct {
	// MaxAttempts is what a new work order allows, zero for the default.
	MaxAttempts int64
	T           testing.TB
	Ctx         context.Context
	DB          Database
	Scope       ledger.WorkspaceID
	Actor       ledger.Actor
	// Wrap decorates every Store handle, which is how a variant breaks one
	// behavior from outside the adapter.
	Wrap func(ports.Store) ports.Store
	// NewOps builds the use cases under test, and a variant replaces one.
	NewOps func(ports.Store) Ops

	trace   Trace
	mu      sync.Mutex
	counter atomic.Int64
	ready   bool
}

const defaultMaxAttempts = 3

// Ops are the use cases the checks drive.
type Ops interface {
	SignIn(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof, ttlMs int64) (control.SessionGrant, error)
	Answer(ctx context.Context, scope ledger.WorkspaceID, id string, answer questions.Answer, actor ledger.Actor) (questions.Decision, error)
	DeliverBatches(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error)
	Create(ctx context.Context, scope ledger.WorkspaceID, draft workorders.WorkOrder, actor ledger.Actor) (workorders.WorkOrder, error)
	Move(ctx context.Context, scope ledger.WorkspaceID, id string, trigger workorders.Trigger, actor ledger.Actor) error
	Rebuild(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) error
	Claim(ctx context.Context, scope ledger.WorkspaceID, request workorders.ClaimRequest, actor ledger.Actor) (workorders.Grant, error)
	Renew(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, ttlMs int64) error
	Sweep(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error)
	Return(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, ttlMs int64, actor ledger.Actor) error
	Release(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, actor ledger.Actor) error
	Reassign(ctx context.Context, scope ledger.WorkspaceID, workOrder, reason string, actor ledger.Actor) error
	StartLane(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, lane string, actor ledger.Actor) (int64, error)
	Write(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, writeID string, payload []byte, actor ledger.Actor) (ledger.Event, error)
	Handoff(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, actor ledger.Actor) (int64, error)
	Dispatch(ctx context.Context, scope ledger.WorkspaceID, workOrder, lane string, actor ledger.Actor) (workorders.Lease, error)
	EndLane(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, result workorders.LaneResult, actor ledger.Actor) error
	Record(ctx context.Context, scope ledger.WorkspaceID, work control.LedgerWork, actor ledger.Actor) (workorders.WorkOrder, error)
	Poll(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource) (control.IntakeReport, error)
	Pass(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (control.ReviewResult, error)
	Promote(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error)
	SendBack(ctx context.Context, scope ledger.WorkspaceID, id, reason string, actor ledger.Actor) error
	RecordQuestion(ctx context.Context, scope ledger.WorkspaceID, draft questions.Question, actor ledger.Actor) (questions.Question, error)
	RecordDecision(ctx context.Context, scope ledger.WorkspaceID, draft questions.Decision, actor ledger.Actor) (questions.Decision, error)
	Dismiss(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) error
	AddCategory(ctx context.Context, scope ledger.WorkspaceID, name string, actor ledger.Actor) error
	Ask(ctx context.Context, scope ledger.WorkspaceID, ask questions.Ask, actor ledger.Actor) (questions.AskResult, error)
	Take(ctx context.Context, scope ledger.WorkspaceID, id string, ttlMs int64, actor ledger.Actor) (questions.Question, error)
	Climb(ctx context.Context, scope ledger.WorkspaceID, id string, step questions.Step, actor ledger.Actor) (questions.Question, error)
	Escalate(ctx context.Context, scope ledger.WorkspaceID, id string, escalation questions.Escalation, actor ledger.Actor) (questions.Question, error)
	Resolve(ctx context.Context, scope ledger.WorkspaceID, id, decision string, actor ledger.Actor) (questions.Question, error)
	Merge(ctx context.Context, scope ledger.WorkspaceID, id, into string, actor ledger.Actor) error
	Rule(ctx context.Context, scope ledger.WorkspaceID, id string, ruling questions.Ruling, actor ledger.Actor) (questions.Decision, error)
	Overturn(ctx context.Context, scope ledger.WorkspaceID, id string, overturn questions.Overturn, actor ledger.Actor) (questions.Overturned, error)
	PublishPolicy(ctx context.Context, scope ledger.WorkspaceID, draft questions.PolicyDraft, actor ledger.Actor) (authority.PolicyVersion, error)
	PolicyVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error)
	Accept(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (workorders.State, error)
	Rework(ctx context.Context, scope ledger.WorkspaceID, id, reason string, actor ledger.Actor) (workorders.State, error)
	Settle(ctx context.Context, scope ledger.WorkspaceID, forge ports.Forge, actor ledger.Actor) ([]string, error)
	Standing(ctx context.Context, scope ledger.WorkspaceID, id string) (workorders.AcceptanceView, error)
	BeginParking(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, wake parking.Draft, actor ledger.Actor) error
	CommitParked(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, commit ledger.ParkedCommit, actor ledger.Actor) (string, error)
	DueWakes(ctx context.Context, scope ledger.WorkspaceID) ([]string, error)
	FireDue(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (bool, error)
	WakeTick(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error)
	Signal(ctx context.Context, scope ledger.WorkspaceID, signal parking.Signal, actor ledger.Actor) ([]string, error)
	ResumeLane(ctx context.Context, scope ledger.WorkspaceID, lane string, actor ledger.Actor) ([]string, error)
}

type realOps struct {
	*control.Sessions
	*control.WorkOrders
	*control.Leases
	*control.Intake
	*control.Reviews
	*control.Questions
	*control.Acceptances
	*control.Wakes
}

// The limits a batch runs under here: a window long enough that a blocking question can
// close it early.
const (
	BatchWindowMs  int64 = 60 * 60 * 1000
	BatchMaxWaitMs int64 = 10 * 60 * 1000
)

// provenMember signs anyone in, so a run tests a session's write and not a slow hash.
type provenMember struct {
	ports.IdentityProvider
}

// Authenticate names the same member for any proof.
func (provenMember) Authenticate(context.Context, ledger.WorkspaceID, ports.Proof) (string, error) {
	return "member-1", nil
}

// RealOps are the production use cases.
func RealOps(store ports.Store) Ops {
	return realOps{
		control.NewSessions(store, provenMember{}), control.NewWorkOrders(store), control.NewLeases(store), control.NewIntake(store), control.NewReviews(store),
		control.NewQuestions(store, questions.BatchPolicy{WindowMs: BatchWindowMs, MaxWaitMs: BatchMaxWaitMs}),
		control.NewAcceptances(store), control.NewWakes(store),
	}
}

// NewRig prepares a rig on a fresh database.
func NewRig(t testing.TB, backend Backend) *Rig {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return &Rig{
		T:      t,
		Ctx:    ctx,
		DB:     backend.Fresh(t),
		Scope:  "workspace-a",
		Actor:  ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"},
		NewOps: RealOps,
	}
}

// StoreWith opens a handle that calls step at each named moment, which is how a
// check stops a transaction at an exact point with nothing compiled into the
// adapter.
func (r *Rig) StoreWith(step func(op, name string)) ports.Store {
	r.T.Helper()
	return Stepped(r.Store(), step)
}

// Store opens a handle. The first handle migrates the database and creates the
// rig's workspace.
func (r *Rig) Store() ports.Store {
	r.T.Helper()
	store := r.DB.Open(r.T, sqlstore.Options{Trace: r.trace.Record})
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ready {
		if err := store.Migrate(r.Ctx); err != nil {
			r.T.Fatalf("migrate: %v", err)
		}
		if _, err := store.CreateWorkspace(r.Ctx, ledger.Workspace{ID: r.Scope, Name: "A"}, r.Actor); err != nil {
			r.T.Fatalf("create workspace: %v", err)
		}
		r.ready = true
	}
	if r.Wrap != nil {
		store = r.Wrap(store)
	}
	return store
}

// ID returns a fresh ID. It stands for the ULID a client generates.
func (r *Rig) ID(prefix string) string {
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s-%s-%x", prefix, strconv.FormatInt(r.counter.Add(1), 10), entropy)
}

// Revision reads the workspace counter.
func (r *Rig) Revision(s ports.Store, scope ledger.WorkspaceID) (int64, error) {
	var rev int64
	err := s.Read(r.Ctx, scope, func(tx ports.ReadTx) error {
		w, err := tx.Workspace()
		rev = w.Rev
		return err
	})
	return rev, err
}

// Events reads the whole log.
func (r *Rig) Events(s ports.Store, scope ledger.WorkspaceID) ([]ledger.Event, error) {
	var events []ledger.Event
	err := s.Read(r.Ctx, scope, func(tx ports.ReadTx) error {
		var err error
		events, err = control.ReadAllEvents(tx.Events)
		return err
	})
	return events, err
}

// Note appends one event that touches nothing else.
func (r *Rig) Note(s ports.Store, scope ledger.WorkspaceID, id string) (ledger.Event, error) {
	var event ledger.Event
	err := s.Write(r.Ctx, scope, "note", func(tx ports.WriteTx) error {
		var err error
		event, err = tx.Append(r.noteDraft(id))
		return err
	})
	return event, err
}

func (r *Rig) noteDraft(id string) ledger.EventDraft {
	return ledger.EventDraft{
		Type:    "note.added",
		Actor:   r.Actor,
		Subject: ledger.Subject{Kind: "note", ID: id},
		Data:    []byte(`{"id":"` + id + `"}`),
	}
}

// NewWorkOrder creates a work order in `intake` through the rig's use cases.
func (r *Rig) NewWorkOrder(s ports.Store) (workorders.WorkOrder, error) {
	attempts := r.MaxAttempts
	if attempts == 0 {
		attempts = defaultMaxAttempts
	}
	return r.NewOps(s).Create(r.Ctx, r.Scope, workorders.WorkOrder{
		ID:           r.ID("wo"),
		Kind:         workorders.KindChange,
		Title:        "Add the retry budget",
		Priority:     2,
		Size:         "medium",
		Requirements: []string{"PAY-12"},
		Needs:        map[string]string{"driver": "claude-code"},
		MaxAttempts:  attempts,
	}, r.Actor)
}

// UsableManifest is a worker that can run what NewWorkOrder needs.
var UsableManifest = ledger.Manifest{Drivers: []ledger.DriverManifest{{
	Driver: "claude-code", Present: true, Qualified: true, SignIn: ledger.SignIn{Kind: ledger.SignInAPIKey, Status: ledger.SignedIn},
}}}

// CheckProjection compares the projection with what the log replays to.
func (r *Rig) CheckProjection(s ports.Store) error {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	replayed, err := workorders.Replay(events)
	if err != nil {
		var invalid *Violation
		if errors.As(err, &invalid) {
			return err
		}
		return Violatedf("the log does not replay: %v", err)
	}
	var rows []workorders.WorkOrder
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		rows, err = tx.WorkOrders().List()
		return err
	})
	if err != nil {
		return err
	}
	if len(rows) != len(replayed) {
		return Violatedf("%d projected work orders, the log replays to %d", len(rows), len(replayed))
	}
	for _, row := range rows {
		want, ok := replayed[row.ID]
		if !ok {
			return Violatedf("work order %s has no history", row.ID)
		}
		if diff := workorders.Diff(row, want); len(diff) > 0 {
			return Violatedf("work order %s disagrees with its log on %v", row.ID, diff)
		}
	}
	return nil
}

// CheckRevisions proves the log is gapless and the counter agrees with it.
func (r *Rig) CheckRevisions(s ports.Store, scope ledger.WorkspaceID) error {
	events, err := r.Events(s, scope)
	if err != nil {
		return err
	}
	for i, e := range events {
		if e.Rev != int64(i+1) {
			return Violatedf("revision gap: event %d has rev %d", i+1, e.Rev)
		}
	}
	rev, err := r.Revision(s, scope)
	if err != nil {
		return err
	}
	if rev != int64(len(events)) {
		return Violatedf("workspace rev %d with %d events", rev, len(events))
	}
	return nil
}

// Run reports one check on a test.
func Run(t *testing.T, backend Backend, checks []Check) {
	t.Helper()
	for _, check := range checks {
		t.Run(check.ID+"-"+check.Name, func(t *testing.T) {
			t.Parallel()
			if err := check.Run(NewRig(t, backend)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
