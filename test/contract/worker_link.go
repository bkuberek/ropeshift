package contract

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/clock"
	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/contextpacks"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// linkTTL and linkHeartbeat are the policy of every link check.
const (
	linkTTL       = int64(60_000)
	linkHeartbeat = int64(15_000)
)

// LinkRig is one control plane and the link a check talks to it through.
type LinkRig struct {
	Store ports.Store
	Scope ledger.WorkspaceID
	Plane *control.Workers
	Link  ports.WorkerLink
	// CorruptNonce answers every heartbeat with someone else's nonce.
	CorruptNonce func()
	// DropHints loses every revision hint.
	DropHints func()
	// ShiftClock moves database time.
	ShiftClock func(deltaMs int64)
}

// LinkBackend makes rigs on one adapter.
type LinkBackend struct {
	Name  string
	Fresh func(t *testing.T, protocol int) *LinkRig
}

// RunWorkerLink runs one suite on one adapter, so the in-memory link cannot drift
// from the HTTP one.
func RunWorkerLink(t *testing.T, backend LinkBackend) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, LinkBackend)
	}{
		{"registration exchanges a join token once", checkRegistration},
		{"a worker asks for a lane it holds", checkWorkerAsk},
		{"a worker reads what the ledger holds about its lane", checkLaneFactsLink},
		{"a worker reads what a lane may reach, under its fence", checkLaneContextLink},
		{"a retried registration replaces the credential", checkRegistrationRetry},
		{"a wrong credential is refused", checkWrongCredential},
		{"a session opens a new epoch and a stale epoch is refused", checkEpochs},
		{"a heartbeat returns its nonce and renews", checkHeartbeat},
		{"a wrong nonce fails", checkWrongNonce},
		{"an empty nonce is refused", checkEmptyNonce},
		{"a lapsed lease returns through a heartbeat", checkLapsedReturns},
		{"an expired lease waits for the sweep", checkExpiredWaitsForTheSweep},
		{"a lane's start and end upload once each", checkLaneUploads},
		{"a lapsed lane's outcome waits for the return", checkLaneEndWaitsForTheReturn},
		{"a fenced lane's outcome is rejected and answered", checkLaneEndFenced},
		{"desired state carries a dispatched lane", checkDesiredLane},
		{"a lost lease comes back as fenced", checkFencedLease},
		{"desired state names this worker's leases", checkDesired},
		{"a hint follows a revision", checkHint},
		{"a lost hint costs latency and nothing else", checkLostHint},
		{"one triple stores one event", checkUploadOnce},
		{"a fenced event gets its own result", checkFencedEvent},
		{"an event keeps when it happened", checkOccurredAt},
		{"an event from an earlier epoch still uploads", checkOldEpochEvent},
		{"one version behind is accepted and older is refused", checkProtocol},
		{"a worker claims through the link", checkWorkerClaim},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.run(t, t.Context(), backend)
		})
	}
}

func (r *LinkRig) register(t *testing.T, ctx context.Context, id string) ledger.WorkerAuth {
	t.Helper()
	token, err := r.Plane.CreateJoinToken(ctx, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := r.Link.Register(ctx, ledger.Registration{JoinToken: token, WorkerID: id, Name: id})
	if err != nil {
		t.Fatal(err)
	}
	if credential.WorkerID != id || credential.Credential == "" {
		t.Fatalf("credential %+v", credential)
	}
	return ledger.WorkerAuth{WorkerID: id, Credential: credential.Credential}
}

func (r *LinkRig) session(t *testing.T, ctx context.Context, auth ledger.WorkerAuth) ledger.Session {
	t.Helper()
	session, err := r.Link.OpenSession(ctx, ledger.SessionRequest{Auth: auth, Protocol: ledger.ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func (r *LinkRig) readyOrder(t *testing.T, ctx context.Context, id string) string {
	t.Helper()
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	_, err := control.NewWorkOrders(r.Store).Record(ctx, r.Scope, control.LedgerWork{
		Order:    workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: "T"},
		Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewReviews(r.Store).Pass(ctx, r.Scope, id, member); err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *LinkRig) claim(t *testing.T, ctx context.Context, id, claim, worker string) workorders.Grant {
	t.Helper()
	grant, err := control.NewLeases(r.Store).Claim(ctx, r.Scope, workorders.ClaimRequest{
		WorkOrder: id, ClaimID: claim, Worker: worker, TTLMs: linkTTL,
	}, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (r *LinkRig) order(t *testing.T, ctx context.Context, id string) workorders.WorkOrder {
	t.Helper()
	var row workorders.WorkOrder
	err := r.Store.Read(ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.WorkOrders().Get(id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func (r *LinkRig) events(t *testing.T, ctx context.Context) []ledger.Event {
	t.Helper()
	var events []ledger.Event
	err := r.Store.Read(ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		events, err = control.ReadAllEvents(tx.Events)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func heldLease(g workorders.Grant) ledger.HeldLease {
	return ledger.HeldLease{WorkOrder: g.WorkOrder, Claim: g.ClaimID, Fence: g.Fence}
}

func checkRegistration(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	token, err := r.Plane.CreateJoinToken(ctx, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Link.Register(ctx, ledger.Registration{JoinToken: token + "x", WorkerID: "a", Name: "a"}); !errors.Is(err, ledger.ErrBadJoinToken) {
		t.Errorf("a wrong token: %v", err)
	}
	if _, err := r.Link.Register(ctx, ledger.Registration{JoinToken: "nothing", WorkerID: "a", Name: "a"}); !errors.Is(err, ledger.ErrBadJoinToken) {
		t.Errorf("no token: %v", err)
	}
	if _, err := r.Link.Register(ctx, ledger.Registration{JoinToken: token, WorkerID: "a", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Link.Register(ctx, ledger.Registration{JoinToken: token, WorkerID: "b", Name: "b"}); !errors.Is(err, ledger.ErrBadJoinToken) {
		t.Errorf("a spent token: %v", err)
	}
	if _, err := r.Plane.CreateJoinToken(ctx, ledger.Actor{Kind: ledger.ActorLane, ID: "lane"}); !errors.Is(err, workorders.ErrForbidden) {
		t.Errorf("a lane made a token: %v", err)
	}
}

func checkRegistrationRetry(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	token, err := r.Plane.CreateJoinToken(ctx, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
	if err != nil {
		t.Fatal(err)
	}
	registration := ledger.Registration{JoinToken: token, WorkerID: "a", Name: "a"}
	first, err := r.Link.Register(ctx, registration)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Link.Register(ctx, registration)
	if err != nil {
		t.Fatal(err)
	}
	if first.Credential == second.Credential {
		t.Error("a retry kept the credential whose answer may have been lost")
	}
	if _, err := r.Link.OpenSession(ctx, ledger.SessionRequest{
		Auth: ledger.WorkerAuth{WorkerID: "a", Credential: first.Credential}, Protocol: ledger.ProtocolVersion,
	}); !errors.Is(err, ledger.ErrBadCredential) {
		t.Errorf("the replaced credential: %v", err)
	}
	r.session(t, ctx, ledger.WorkerAuth{WorkerID: "a", Credential: second.Credential})
}

func checkWrongCredential(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	wrong := ledger.WorkerAuth{WorkerID: "a", Credential: "wrong"}
	if _, err := r.Link.OpenSession(ctx, ledger.SessionRequest{Auth: wrong, Protocol: ledger.ProtocolVersion}); !errors.Is(err, ledger.ErrBadCredential) {
		t.Errorf("open a session: %v", err)
	}
	if _, err := r.Link.Heartbeat(ctx, ledger.Heartbeat{Auth: wrong, Epoch: session.Epoch, Nonce: "n"}); !errors.Is(err, ledger.ErrBadCredential) {
		t.Errorf("a heartbeat: %v", err)
	}
	if _, err := r.Link.Desired(ctx, ledger.DesiredRequest{Auth: wrong}); !errors.Is(err, ledger.ErrBadCredential) {
		t.Errorf("desired state: %v", err)
	}
	if _, err := r.Link.Upload(ctx, ledger.UploadBatch{Auth: wrong, Epoch: session.Epoch}); !errors.Is(err, ledger.ErrBadCredential) {
		t.Errorf("an upload: %v", err)
	}
	if _, err := r.Link.Watch(ctx, ledger.WatchRequest{Auth: wrong}); !errors.Is(err, ledger.ErrBadCredential) {
		t.Errorf("a watch: %v", err)
	}
	unknown := ledger.WorkerAuth{WorkerID: "nobody", Credential: auth.Credential}
	if _, err := r.Link.Heartbeat(ctx, ledger.Heartbeat{Auth: unknown, Epoch: 1, Nonce: "n"}); !errors.Is(err, ledger.ErrBadCredential) {
		t.Errorf("an unknown worker: %v", err)
	}
}

func checkEpochs(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	first := r.session(t, ctx, auth)
	second := r.session(t, ctx, auth)
	if second.Epoch <= first.Epoch {
		t.Fatalf("epochs %d then %d", first.Epoch, second.Epoch)
	}
	if first.HeartbeatMs != linkHeartbeat || first.LeaseTTLMs != linkTTL {
		t.Errorf("session %+v", first)
	}
	if _, err := r.Link.Heartbeat(ctx, ledger.Heartbeat{Auth: auth, Epoch: first.Epoch, Nonce: "n"}); !errors.Is(err, ledger.ErrStaleEpoch) {
		t.Errorf("a stale heartbeat: %v", err)
	}
	if _, err := r.Link.Upload(ctx, ledger.UploadBatch{Auth: auth, Epoch: first.Epoch}); !errors.Is(err, ledger.ErrStaleEpoch) {
		t.Errorf("a stale upload: %v", err)
	}
}

func checkHeartbeat(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	grant := r.claim(t, ctx, r.readyOrder(t, ctx, "wo"), "c1", "a")
	before := r.order(t, ctx, "wo").LeaseUntil
	revision := r.events(t, ctx)
	r.ShiftClock(1000)
	reply, err := r.Link.Heartbeat(ctx, ledger.Heartbeat{
		Auth: auth, Epoch: session.Epoch, Protocol: ledger.ProtocolVersion, Nonce: "nonce-1",
		Leases: []ledger.HeldLease{heldLease(grant)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Nonce != "nonce-1" {
		t.Errorf("nonce %q", reply.Nonce)
	}
	if reply.ServerTimeMs == 0 || reply.DesiredRev == 0 || reply.FleetMode != ledger.FleetRun {
		t.Errorf("reply %+v", reply)
	}
	if len(reply.Leases) != 1 || reply.Leases[0].TTLMs != linkTTL || reply.Leases[0].Fence != grant.Fence {
		t.Errorf("leases %+v", reply.Leases)
	}
	if after := r.order(t, ctx, "wo").LeaseUntil; after <= before {
		t.Errorf("the deadline stayed at %d", after)
	}
	if got := r.events(t, ctx); len(got) != len(revision) {
		t.Errorf("a heartbeat appended %d events", len(got)-len(revision))
	}
}

func checkWrongNonce(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	subject := newLinkWorker(t, ctx, r)
	r.CorruptNonce()
	if _, err := subject.Beat(ctx); !errors.Is(err, ledger.ErrNonce) {
		t.Errorf("a wrong nonce: %v", err)
	}
}

func checkEmptyNonce(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	_, err := r.Link.Heartbeat(ctx, ledger.Heartbeat{Auth: auth, Epoch: session.Epoch})
	if err == nil {
		t.Error("a heartbeat with no nonce was answered")
	}
}

func newLinkWorker(t *testing.T, ctx context.Context, r *LinkRig) *worker.Worker {
	t.Helper()
	journal, err := fake.NewJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	subject := worker.New(r.Link, journal, clock.System{}, worker.Config{
		Name: "subject",
		JoinToken: func(ctx context.Context) (string, error) {
			return r.Plane.CreateJoinToken(ctx, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
		},
	})
	if err := subject.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return subject
}

func checkLapsedReturns(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	grant := r.claim(t, ctx, r.readyOrder(t, ctx, "wo"), "c1", "a")
	r.ShiftClock(linkTTL + 1)
	actor := ledger.Actor{Kind: ledger.ActorSystem, ID: "tick"}
	if _, err := control.NewLeases(r.Store).Sweep(ctx, r.Scope, actor); err != nil {
		t.Fatal(err)
	}
	if row := r.order(t, ctx, "wo"); row.State != workorders.Lapsed {
		t.Fatalf("row %+v", row)
	}
	reply, err := r.Link.Heartbeat(ctx, ledger.Heartbeat{
		Auth: auth, Epoch: session.Epoch, Nonce: "n", Leases: []ledger.HeldLease{heldLease(grant)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Leases) != 1 || len(reply.Fenced) != 0 {
		t.Errorf("reply %+v", reply)
	}
	row := r.order(t, ctx, "wo")
	if row.State != workorders.Claimed || row.Fence != grant.Fence {
		t.Errorf("after the return %+v", row)
	}
}

// checkExpiredWaitsForTheSweep beats after the deadline and before the sweep. The
// answer neither renews nor fences the lease, so the worker keeps it, and the next
// beat after the sweep returns it.
func checkExpiredWaitsForTheSweep(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	grant := r.claim(t, ctx, r.readyOrder(t, ctx, "wo"), "c1", "a")
	r.ShiftClock(linkTTL + 1)
	beat := ledger.Heartbeat{Auth: auth, Epoch: session.Epoch, Nonce: "n", Leases: []ledger.HeldLease{heldLease(grant)}}
	reply, err := r.Link.Heartbeat(ctx, beat)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Leases) != 0 || len(reply.Fenced) != 0 {
		t.Errorf("before the sweep: %+v", reply)
	}
	actor := ledger.Actor{Kind: ledger.ActorSystem, ID: "tick"}
	if _, err := control.NewLeases(r.Store).Sweep(ctx, r.Scope, actor); err != nil {
		t.Fatal(err)
	}
	if reply, err = r.Link.Heartbeat(ctx, beat); err != nil {
		t.Fatal(err)
	}
	if len(reply.Leases) != 1 || len(reply.Fenced) != 0 {
		t.Errorf("after the sweep: %+v", reply)
	}
	if row := r.order(t, ctx, "wo"); row.State != workorders.Claimed || row.Fence != grant.Fence {
		t.Errorf("after the return %+v", row)
	}
}

func laneEvents(grant workorders.Grant, epoch int64, outcome workorders.Outcome) []ledger.UploadEvent {
	base := ledger.UploadEvent{
		Epoch: epoch, WorkOrder: grant.WorkOrder, ClaimID: grant.ClaimID, Fence: grant.Fence, Lane: "lane-1", Generation: 1, OccurredAt: 1,
	}
	started, ended := base, base
	started.Seq, started.Type = 1, ledger.UploadLaneStarted
	ended.Seq, ended.Type = 2, ledger.UploadLaneEnded
	ended.Payload, _ = json.Marshal(workorders.LaneResult{Report: workorders.Report{Outcome: outcome}})
	return []ledger.UploadEvent{started, ended}
}

func (r *LinkRig) laneEndCount(t *testing.T, ctx context.Context, trigger workorders.Trigger) int {
	t.Helper()
	seen := 0
	for _, e := range r.events(t, ctx) {
		if e.Type == workorders.EventType(trigger) {
			seen++
		}
	}
	return seen
}

func checkLaneUploads(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	grant := r.claim(t, ctx, r.readyOrder(t, ctx, "wo"), "c1", "a")
	batch := ledger.UploadBatch{Auth: auth, Epoch: session.Epoch, Events: laneEvents(grant, session.Epoch, workorders.OutcomeCompleted)}
	for range 2 {
		reply, err := r.Link.Upload(ctx, batch)
		if err != nil {
			t.Fatal(err)
		}
		if reply.HighWater != 2 || len(reply.Rejected) != 0 {
			t.Fatalf("reply %+v", reply)
		}
	}
	if row := r.order(t, ctx, "wo"); row.State != workorders.Acceptance || row.Fence <= grant.Fence {
		t.Errorf("row %+v", row)
	}
	if got := r.laneEndCount(t, ctx, workorders.TriggerLaneCompleted); got != 1 {
		t.Errorf("%d outcomes on record", got)
	}
	if got := r.laneEndCount(t, ctx, workorders.TriggerLaneStarted); got != 1 {
		t.Errorf("%d starts on record", got)
	}
}

func checkLaneEndWaitsForTheReturn(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	grant := r.claim(t, ctx, r.readyOrder(t, ctx, "wo"), "c1", "a")
	events := laneEvents(grant, session.Epoch, workorders.OutcomeCompleted)
	if _, err := r.Link.Upload(ctx, ledger.UploadBatch{Auth: auth, Epoch: session.Epoch, Events: events[:1]}); err != nil {
		t.Fatal(err)
	}
	r.ShiftClock(linkTTL + 1)
	if _, err := control.NewLeases(r.Store).Sweep(ctx, r.Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "tick"}); err != nil {
		t.Fatal(err)
	}
	reply, err := r.Link.Upload(ctx, ledger.UploadBatch{Auth: auth, Epoch: session.Epoch, Events: events[1:]})
	if err != nil {
		t.Fatal(err)
	}
	if reply.HighWater != 0 || len(reply.Rejected) != 0 {
		t.Fatalf("a lapsed outcome was answered: %+v", reply)
	}
	if row := r.order(t, ctx, "wo"); row.State != workorders.Lapsed {
		t.Fatalf("row %+v", row)
	}
	beat := ledger.Heartbeat{Auth: auth, Epoch: session.Epoch, Nonce: "n", Leases: []ledger.HeldLease{heldLease(grant)}}
	if _, err := r.Link.Heartbeat(ctx, beat); err != nil {
		t.Fatal(err)
	}
	if reply, err = r.Link.Upload(ctx, ledger.UploadBatch{Auth: auth, Epoch: session.Epoch, Events: events[1:]}); err != nil {
		t.Fatal(err)
	}
	if reply.HighWater != 2 {
		t.Fatalf("reply %+v", reply)
	}
	if row := r.order(t, ctx, "wo"); row.State != workorders.Acceptance {
		t.Errorf("row %+v", row)
	}
}

func checkLaneEndFenced(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	grant := r.claim(t, ctx, r.readyOrder(t, ctx, "wo"), "c1", "a")
	events := laneEvents(grant, session.Epoch, workorders.OutcomeCompleted)
	if _, err := r.Link.Upload(ctx, ledger.UploadBatch{Auth: auth, Epoch: session.Epoch, Events: events[:1]}); err != nil {
		t.Fatal(err)
	}
	r.ShiftClock(linkTTL + 1)
	leases := control.NewLeases(r.Store)
	if _, err := leases.Sweep(ctx, r.Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "tick"}); err != nil {
		t.Fatal(err)
	}
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if err := leases.Reassign(ctx, r.Scope, "wo", "a closed laptop", member); err != nil {
		t.Fatal(err)
	}
	reply, err := r.Link.Upload(ctx, ledger.UploadBatch{Auth: auth, Epoch: session.Epoch, Events: events[1:]})
	if err != nil {
		t.Fatal(err)
	}
	if reply.HighWater != 2 || len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadFenced {
		t.Fatalf("reply %+v", reply)
	}
	if row := r.order(t, ctx, "wo"); row.State != workorders.Ready {
		t.Errorf("row %+v", row)
	}
}

func checkDesiredLane(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	r.session(t, ctx, auth)
	r.claim(t, ctx, r.readyOrder(t, ctx, "wo"), "c1", "a")
	state, err := r.Link.Desired(ctx, ledger.DesiredRequest{Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Leases) != 1 || state.Leases[0].Lane != "" {
		t.Fatalf("a claim with no dispatch names a lane: %+v", state.Leases)
	}
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if _, err := control.NewLeases(r.Store).Dispatch(ctx, r.Scope, "wo", "lane-1", member); err != nil {
		t.Fatal(err)
	}
	state, err = r.Link.Desired(ctx, ledger.DesiredRequest{Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Leases) != 1 || state.Leases[0].Lane != "lane-1" || state.Leases[0].State != string(workorders.Claimed) {
		t.Fatalf("leases %+v", state.Leases)
	}
}

func checkFencedLease(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	grant := r.claim(t, ctx, r.readyOrder(t, ctx, "wo"), "c1", "a")
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if err := control.NewLeases(r.Store).Release(ctx, r.Scope, grant.Lease, member); err != nil {
		t.Fatal(err)
	}
	reply, err := r.Link.Heartbeat(ctx, ledger.Heartbeat{
		Auth: auth, Epoch: session.Epoch, Nonce: "n", Leases: []ledger.HeldLease{heldLease(grant)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Leases) != 0 || len(reply.Fenced) != 1 || reply.Fenced[0].CurrentFence <= grant.Fence {
		t.Errorf("reply %+v", reply)
	}
}

func checkDesired(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	r.session(t, ctx, auth)
	grant := r.claim(t, ctx, r.readyOrder(t, ctx, "mine"), "c1", "a")
	r.claim(t, ctx, r.readyOrder(t, ctx, "theirs"), "c2", "b")
	state, err := r.Link.Desired(ctx, ledger.DesiredRequest{Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Leases) != 1 || state.Leases[0].WorkOrder != "mine" || state.Leases[0].ClaimID != "c1" ||
		state.Leases[0].Fence != grant.Fence || state.Rev == 0 || state.FleetMode != ledger.FleetRun {
		t.Errorf("state %+v", state)
	}
	again, err := r.Link.Desired(ctx, ledger.DesiredRequest{Auth: auth, Since: state.Rev})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Unchanged || again.Rev != state.Rev {
		t.Errorf("a read at the same revision %+v", again)
	}
}

func checkHint(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	r.session(t, ctx, auth)
	state, err := r.Link.Desired(ctx, ledger.DesiredRequest{Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	watching, stop := context.WithCancel(ctx)
	defer stop()
	hints, err := r.Link.Watch(watching, ledger.WatchRequest{Auth: auth, After: state.Rev})
	if err != nil {
		t.Fatal(err)
	}
	r.readyOrder(t, ctx, "wo")
	select {
	case rev := <-hints:
		if rev <= state.Rev {
			t.Errorf("a hint at %d after %d", rev, state.Rev)
		}
	case <-time.After(10 * time.Second):
		t.Error("no hint arrived")
	}
}

func checkLostHint(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	subject := newLinkWorker(t, ctx, r)
	r.DropHints()
	watching, stop := context.WithCancel(ctx)
	defer stop()
	hints, err := r.Link.Watch(watching, ledger.WatchRequest{Auth: subject.Auth()})
	if err != nil {
		t.Fatal(err)
	}
	grant := r.claim(t, ctx, r.readyOrder(t, ctx, "wo"), "c1", subject.Auth().WorkerID)
	select {
	case hint, open := <-hints:
		if open {
			t.Fatalf("a hint reached the worker: %d", hint)
		}
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := subject.Beat(ctx); err != nil {
		t.Fatal(err)
	}
	reply, err := subject.Beat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Leases) != 1 || reply.Leases[0].WorkOrder != grant.WorkOrder {
		t.Errorf("the worker did not converge: %+v", reply)
	}
}

func uploadOne(t *testing.T, ctx context.Context, r *LinkRig, auth ledger.WorkerAuth, epoch int64, e ledger.UploadEvent) ledger.UploadReply {
	t.Helper()
	reply, err := r.Link.Upload(ctx, ledger.UploadBatch{Auth: auth, Epoch: epoch, Events: []ledger.UploadEvent{e}})
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func checkUploadOnce(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	grant := r.claim(t, ctx, r.readyOrder(t, ctx, "wo"), "c1", "a")
	event := ledger.UploadEvent{
		Epoch: session.Epoch, Seq: 7, Type: "note", WorkOrder: "wo", ClaimID: grant.ClaimID, Fence: grant.Fence,
		Payload: []byte("once"), OccurredAt: 1000,
	}
	first := uploadOne(t, ctx, r, auth, session.Epoch, event)
	before := len(r.events(t, ctx))
	rev := r.order(t, ctx, "wo").Rev
	second := uploadOne(t, ctx, r, auth, session.Epoch, event)
	if first.HighWater != 7 || second.HighWater != 7 || len(first.Rejected)+len(second.Rejected) != 0 {
		t.Errorf("replies %+v and %+v", first, second)
	}
	if after := len(r.events(t, ctx)); after != before {
		t.Errorf("a retry appended %d events", after-before)
	}
	if after := r.order(t, ctx, "wo").Rev; after != rev {
		t.Errorf("a retry moved the write count from %d to %d", rev, after)
	}
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if err := control.NewLeases(r.Store).Release(ctx, r.Scope, grant.Lease, member); err != nil {
		t.Fatal(err)
	}
	late := uploadOne(t, ctx, r, auth, session.Epoch, event)
	if len(late.Rejected) != 0 {
		t.Errorf("a retry after the fence moved was rejected: %+v", late)
	}
}

func checkFencedEvent(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	stale := r.claim(t, ctx, r.readyOrder(t, ctx, "stale"), "c1", "a")
	live := r.claim(t, ctx, r.readyOrder(t, ctx, "live"), "c2", "a")
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if err := control.NewLeases(r.Store).Release(ctx, r.Scope, stale.Lease, member); err != nil {
		t.Fatal(err)
	}
	reply, err := r.Link.Upload(ctx, ledger.UploadBatch{Auth: auth, Epoch: session.Epoch, Events: []ledger.UploadEvent{
		{Epoch: session.Epoch, Seq: 1, Type: "note", WorkOrder: "live", ClaimID: live.ClaimID, Fence: live.Fence, OccurredAt: 1},
		{Epoch: session.Epoch, Seq: 2, Type: "note", WorkOrder: "stale", ClaimID: stale.ClaimID, Fence: stale.Fence, OccurredAt: 2},
		{Epoch: session.Epoch, Seq: 3, Type: "note", WorkOrder: "live", ClaimID: live.ClaimID, Fence: live.Fence, OccurredAt: 3},
		{Epoch: session.Epoch, Seq: 4, Type: "note", WorkOrder: "absent", ClaimID: "c", Fence: 1, OccurredAt: 4},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if reply.HighWater != 4 || len(reply.Rejected) != 2 {
		t.Fatalf("reply %+v", reply)
	}
	fenced := reply.Rejected[0]
	if fenced.Seq != 2 || fenced.Status != ledger.UploadFenced || fenced.CurrentFence <= stale.Fence || fenced.State == "" {
		t.Errorf("result %+v", fenced)
	}
	if invalid := reply.Rejected[1]; invalid.Seq != 4 || invalid.Status != ledger.UploadInvalid {
		t.Errorf("result %+v", invalid)
	}
	stored := 0
	for _, e := range r.events(t, ctx) {
		if strings.HasPrefix(e.ID, "a:") {
			stored++
		}
	}
	if stored != 2 {
		t.Errorf("%d events stored, want 2", stored)
	}
}

func checkOccurredAt(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	grant := r.claim(t, ctx, r.readyOrder(t, ctx, "wo"), "c1", "a")
	uploadOne(t, ctx, r, auth, session.Epoch, ledger.UploadEvent{
		Epoch: session.Epoch, Seq: 1, Type: "note", WorkOrder: "wo", ClaimID: grant.ClaimID, Fence: grant.Fence, OccurredAt: 1234,
	})
	events := r.events(t, ctx)
	last := events[len(events)-1]
	if last.OccurredAt != 1234 || last.RecordedAt == 0 || last.RecordedAt == 1234 {
		t.Errorf("event %+v", last)
	}
	if last.Actor.Kind != ledger.ActorWorker || last.Actor.ID != "a" {
		t.Errorf("actor %+v", last.Actor)
	}
}

func checkOldEpochEvent(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	first := r.session(t, ctx, auth)
	grant := r.claim(t, ctx, r.readyOrder(t, ctx, "wo"), "c1", "a")
	second := r.session(t, ctx, auth)
	reply := uploadOne(t, ctx, r, auth, second.Epoch, ledger.UploadEvent{
		Epoch: first.Epoch, Seq: 1, Type: "note", WorkOrder: "wo", ClaimID: grant.ClaimID, Fence: grant.Fence, OccurredAt: 1,
	})
	if len(reply.Rejected) != 0 || reply.HighWater != 1 {
		t.Errorf("reply %+v", reply)
	}
}

func checkProtocol(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 3)
	auth := r.register(t, ctx, "a")
	if _, err := r.Link.OpenSession(ctx, ledger.SessionRequest{Auth: auth, Protocol: 3}); err != nil {
		t.Errorf("the same version: %v", err)
	}
	behind, err := r.Link.OpenSession(ctx, ledger.SessionRequest{Auth: auth, Protocol: 2})
	if err != nil {
		t.Fatalf("one behind: %v", err)
	}
	if behind.Protocol != 2 {
		t.Errorf("negotiated %d", behind.Protocol)
	}
	_, err = r.Link.OpenSession(ctx, ledger.SessionRequest{Auth: auth, Protocol: 1})
	var refused *ledger.ProtocolError
	if !errors.As(err, &refused) {
		t.Fatalf("two behind: %v", err)
	}
	if refused.Worker != 1 || refused.Oldest != 2 || refused.Newest != 3 || !strings.Contains(err.Error(), "upgrade the worker") {
		t.Errorf("refusal %+v: %v", refused, err)
	}
}

func checkWorkerClaim(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	r.session(t, ctx, auth)
	id := r.readyOrder(t, ctx, "wo")
	request := workorders.ClaimRequest{WorkOrder: id, ClaimID: "c1", TTLMs: linkTTL}
	first, err := r.Link.Claim(ctx, auth, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Link.Claim(ctx, auth, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fence != second.Fence || !second.Replayed {
		t.Errorf("first %+v, retry %+v", first, second)
	}
	if row := r.order(t, ctx, id); row.LeaseWorker != "a" {
		t.Errorf("row %+v", row)
	}
	wrong := ledger.WorkerAuth{WorkerID: "a", Credential: "wrong"}
	if _, err := r.Link.Claim(ctx, wrong, workorders.ClaimRequest{WorkOrder: id, ClaimID: "c2"}); !errors.Is(err, ledger.ErrBadCredential) {
		t.Errorf("a wrong credential: %v", err)
	}
	if _, err := r.Link.Claim(ctx, auth, workorders.ClaimRequest{WorkOrder: id, ClaimID: "c3"}); !errors.Is(err, workorders.ErrLost) {
		t.Errorf("a lost claim: %v", err)
	}
}

func checkLaneFactsLink(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	id := r.readyOrder(t, ctx, "wo")
	grant, err := r.Link.Claim(ctx, auth, workorders.ClaimRequest{WorkOrder: id, ClaimID: "c1", TTLMs: linkTTL})
	if err != nil {
		t.Fatal(err)
	}
	request := ledger.LaneFactsRequest{Auth: auth, Epoch: session.Epoch, WorkOrder: id, Fence: grant.Fence, Lane: "lane-1"}
	empty, err := r.Link.LaneFacts(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if empty.OpenQuestions == nil || empty.OpenAssumptions == nil || empty.Checks == nil || len(empty.OpenQuestions) != 0 {
		t.Errorf("a list read as empty must say so: %+v", empty)
	}
	_, err = r.Link.Ask(ctx, questions.LaneAsk{
		Auth: auth, Epoch: session.Epoch, WorkOrder: id, Fence: grant.Fence,
		Ask: questions.Ask{ID: "q1", Text: "What is the retry budget?", Lane: "lane-1", Generation: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := r.Link.LaneFacts(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.OpenQuestions) != 1 || facts.OpenQuestions[0] != "q1" {
		t.Errorf("facts %+v", facts)
	}
	other := request
	other.Lane = "lane-2"
	if facts, err := r.Link.LaneFacts(ctx, other); err != nil || len(facts.OpenQuestions) != 0 {
		t.Errorf("another lane's question: %+v, %v", facts, err)
	}
	stale := request
	stale.Fence = grant.Fence + 1
	var fenced *workorders.FencedError
	if _, err := r.Link.LaneFacts(ctx, stale); !errors.As(err, &fenced) {
		t.Errorf("a stale fence: %v", err)
	}
}

func checkLaneContextLink(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	id := r.readyOrder(t, ctx, "wo")
	grant, err := r.Link.Claim(ctx, auth, workorders.ClaimRequest{WorkOrder: id, ClaimID: "c1", TTLMs: linkTTL})
	if err != nil {
		t.Fatal(err)
	}
	draft := questions.Decision{ID: "d-1", Status: questions.DecisionDecided, Statement: "Use two retries."}
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if _, err := control.NewQuestions(r.Store, questions.BatchPolicy{}).RecordDecision(ctx, r.Scope, draft, member); err != nil {
		t.Fatal(err)
	}
	request := ledger.LaneContextRequest{
		Auth: auth, Epoch: session.Epoch, WorkOrder: id, Fence: grant.Fence, Lane: "lane-1",
		Tool: lanes.ToolContextGet, Kind: string(contextpacks.KindDecision), Ref: draft.ID,
	}
	found, err := r.Link.LaneContext(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Records) != 1 || !strings.Contains(found.Records[0].Text, draft.Statement) {
		t.Errorf("found %+v", found)
	}
	missing := request
	missing.Ref = "d-none"
	if none, err := r.Link.LaneContext(ctx, missing); err != nil || none.Records == nil || len(none.Records) != 0 {
		t.Errorf("a miss must say so: %+v, %v", none, err)
	}
	stale := request
	stale.Fence = grant.Fence + 1
	var fenced *workorders.FencedError
	if _, err := r.Link.LaneContext(ctx, stale); !errors.As(err, &fenced) {
		t.Errorf("a stale fence: %v", err)
	}
	unknown := request
	unknown.Tool = "lane_delete"
	if _, err := r.Link.LaneContext(ctx, unknown); !errors.Is(err, workorders.ErrInvalid) {
		t.Errorf("a tool nobody named: %v", err)
	}
}

func checkWorkerAsk(t *testing.T, ctx context.Context, b LinkBackend) {
	r := b.Fresh(t, 0)
	auth := r.register(t, ctx, "a")
	session := r.session(t, ctx, auth)
	id := r.readyOrder(t, ctx, "wo")
	grant, err := r.Link.Claim(ctx, auth, workorders.ClaimRequest{WorkOrder: id, ClaimID: "c1", TTLMs: linkTTL})
	if err != nil {
		t.Fatal(err)
	}
	ask := questions.LaneAsk{
		Auth: auth, Epoch: session.Epoch, WorkOrder: id, Fence: grant.Fence,
		Ask: questions.Ask{ID: "q1", Text: "What is the retry budget?", Lane: "lane-1", Generation: 1},
	}
	first, err := r.Link.Ask(ctx, ask)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != questions.AskPending || first.QuestionID != "q1" {
		t.Errorf("first %+v", first)
	}
	ask.Ask.ID, ask.Ask.Lane = "q2", "lane-2"
	second, err := r.Link.Ask(ctx, ask)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != questions.AskAttached || second.QuestionID != "q1" {
		t.Errorf("second %+v", second)
	}
	stale := ask
	stale.Fence = grant.Fence + 1
	var fenced *workorders.FencedError
	if _, err := r.Link.Ask(ctx, stale); !errors.As(err, &fenced) {
		t.Errorf("a stale fence: %v", err)
	}
	other := r.register(t, ctx, "b")
	thief := ask
	thief.Auth, thief.Epoch = other, r.session(t, ctx, other).Epoch
	if _, err := r.Link.Ask(ctx, thief); !errors.As(err, &fenced) {
		t.Errorf("a worker that holds nothing: %v", err)
	}
	wrong := ask
	wrong.Auth.Credential = "wrong"
	if _, err := r.Link.Ask(ctx, wrong); !errors.Is(err, ledger.ErrBadCredential) {
		t.Errorf("a wrong credential: %v", err)
	}
	empty := ask
	empty.Ask.ID, empty.Ask.Text = "q3", " ?! "
	if _, err := r.Link.Ask(ctx, empty); !errors.Is(err, questions.ErrInvalid) {
		t.Errorf("an empty question: %v", err)
	}
}
