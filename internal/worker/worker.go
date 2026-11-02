package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

const (
	defaultBatch = 100
	// retryEvery bounds how long a worker waits to try again after a failure, so a
	// control plane that returns is noticed well before the next heartbeat.
	retryEvery = time.Second
)

// Config is what a worker is told at start.
type Config struct {
	// Name is the worker's name in the ledger.
	Name string
	// JoinToken supplies the token on a first start. Solo mode makes one, and a
	// container reads it from its environment. A registered worker never calls it.
	JoinToken func(context.Context) (string, error)
	// Protocol is the version this worker speaks. Zero means this build's.
	Protocol int
	// Manifest is what it advertises beyond its drivers: its owner, capacity, and labels.
	Manifest ledger.Manifest
	// BatchSize bounds one upload.
	BatchSize int
	// Spool is the file hooks append to when the worker is down.
	Spool string
	// Report receives what Run could not do, because Run outlives any one failure.
	Report func(error)
	// Lanes runs the lanes a member dispatches. A worker with none only holds leases.
	Lanes *LaneRuntime
}

// Worker holds the machine's connection to the control plane. Everything it must
// remember is in the journal, so a restart works from the journal alone.
type Worker struct {
	link    ports.WorkerLink
	journal ports.Journal
	clock   ports.Clock
	config  Config

	// wakeLock guards wakes, which a pass and an orchestrator's run both read.
	wakeLock sync.Mutex
	wakes    lanes.WakeBatch

	mu         sync.Mutex
	identity   ledger.WorkerIdentity
	session    ledger.Session
	desiredRev int64
	// advertised is the manifest this epoch opened with.
	advertised ledger.Manifest
	deadlines  map[string]time.Time
	running    map[string]*laneRun
	published  map[string]publishedStatus
	// holding names what each lane was last held back by, so a note goes out once.
	holding map[string]string

	uploading sync.Mutex
	// sign is what this worker knows of its sign-in, as the journal holds it, and polledAt is when
	// it last asked the doctor. signLock guards both, and is held across the journal write, so two
	// lanes that reach one limit take turns.
	signLock  sync.Mutex
	sign      lanes.SignInState
	polledAt  time.Time
	laneGroup sync.WaitGroup
	marked    sync.Once
}

// New returns a worker that has not started.
func New(link ports.WorkerLink, journal ports.Journal, clock ports.Clock, config Config) *Worker {
	if config.Protocol == 0 {
		config.Protocol = ledger.ProtocolVersion
	}
	if config.BatchSize == 0 {
		config.BatchSize = defaultBatch
	}
	if config.Report == nil {
		config.Report = func(error) {}
	}
	return &Worker{
		link: link, journal: journal, clock: clock, config: config,
		deadlines: map[string]time.Time{}, running: map[string]*laneRun{}, published: map[string]publishedStatus{},
		holding: map[string]string{},
	}
}

func newToken(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// Start registers on a first run and opens a new epoch. The worker's ID is on disk
// before it asks, so a crash between the exchange and saving the answer repeats the
// same registration.
func (w *Worker) Start(ctx context.Context) error {
	identity, err := w.journal.Identity(ctx)
	if errors.Is(err, ports.ErrNotFound) {
		if identity.WorkerID, err = newToken(16); err != nil {
			return err
		}
		if err := w.journal.SaveIdentity(ctx, identity); err != nil {
			return err
		}
		crashpoint.Hit("worker.start", "identified")
	} else if err != nil {
		return err
	}
	if identity.Credential == "" {
		if err := w.register(ctx, &identity); err != nil {
			return err
		}
	}
	manifest := w.manifest(ctx)
	session, err := w.link.OpenSession(ctx, ledger.SessionRequest{
		Auth:     ledger.WorkerAuth{WorkerID: identity.WorkerID, Credential: identity.Credential},
		Protocol: w.config.Protocol,
		Manifest: manifest,
	})
	if err != nil {
		return err
	}
	identity.Epoch = session.Epoch
	if err := w.journal.SaveIdentity(ctx, identity); err != nil {
		return err
	}
	if err := w.DrainSpool(ctx); err != nil {
		return err
	}
	cached, err := w.journal.Desired(ctx)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	if err := w.loadSignIn(ctx); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.identity, w.session, w.desiredRev, w.advertised = identity, session, cached.Rev, manifest
	return nil
}

// manifest asks the driver what this machine can run. A doctor that fails advertises no
// driver and stops nothing, because a worker must still hold what it holds.
func (w *Worker) manifest(ctx context.Context) ledger.Manifest {
	if w.config.Lanes == nil {
		return w.config.Manifest
	}
	manifest, err := BuildManifest(ctx, w.config.Lanes.Driver, w.config.Manifest)
	if err != nil {
		w.config.Report(fmt.Errorf("the doctor: %w", err))
		return w.config.Manifest
	}
	return manifest
}

// signInKind says how the driver this epoch advertised is signed in.
func (w *Worker) signInKind() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.advertised.Drivers) == 0 {
		return ""
	}
	return w.advertised.Drivers[0].SignIn.Kind
}

func (w *Worker) register(ctx context.Context, identity *ledger.WorkerIdentity) error {
	if w.config.JoinToken == nil {
		return errors.New("this worker is not registered and has no join token")
	}
	token, err := w.config.JoinToken(ctx)
	if err != nil {
		return err
	}
	credential, err := w.link.Register(ctx, ledger.Registration{
		JoinToken: token, WorkerID: identity.WorkerID, Name: w.config.Name,
	})
	if err != nil {
		return err
	}
	crashpoint.Hit("worker.start", "exchanged")
	identity.Credential = credential.Credential
	return w.journal.SaveIdentity(ctx, *identity)
}

// Auth returns this worker's identity on the plane.
func (w *Worker) Auth() ledger.WorkerAuth {
	w.mu.Lock()
	defer w.mu.Unlock()
	return ledger.WorkerAuth{WorkerID: w.identity.WorkerID, Credential: w.identity.Credential}
}

func (w *Worker) epoch() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.identity.Epoch
}

// Session returns what the control plane decided for this epoch.
func (w *Worker) Session() ledger.Session {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.session
}

// Beat runs one heartbeat. Only an answer carrying this nonce proves anything, so a
// wrong one changes nothing here. A revision the worker has not read means desired
// state moved, so a lost hint costs at most one beat.
func (w *Worker) Beat(ctx context.Context) (ledger.HeartbeatReply, error) {
	leases, err := w.journal.Leases(ctx)
	if err != nil {
		return ledger.HeartbeatReply{}, err
	}
	through, err := w.journal.Through(ctx)
	if err != nil {
		return ledger.HeartbeatReply{}, err
	}
	nonce, err := newToken(8)
	if err != nil {
		return ledger.HeartbeatReply{}, err
	}
	w.mu.Lock()
	auth := ledger.WorkerAuth{WorkerID: w.identity.WorkerID, Credential: w.identity.Credential}
	epoch, desired := w.identity.Epoch, w.desiredRev
	w.mu.Unlock()
	sent := w.clock.Now()
	reply, err := w.link.Heartbeat(ctx, ledger.Heartbeat{
		Auth: auth, Epoch: epoch, Protocol: w.config.Protocol, Nonce: nonce,
		DesiredRev: desired, EventsThrough: through, Leases: leases,
	})
	if err != nil {
		return ledger.HeartbeatReply{}, err
	}
	if reply.Nonce != nonce {
		return ledger.HeartbeatReply{}, fmt.Errorf("%w: sent %s, got %s", ledger.ErrNonce, nonce, reply.Nonce)
	}
	if err := w.applyBeat(ctx, leases, reply, sent); err != nil {
		return reply, err
	}
	if reply.DesiredRev != desired {
		return reply, w.Sync(ctx)
	}
	return reply, nil
}

// applyBeat keeps what the answer says. A lease's deadline counts from the moment
// the request left, which errs on the safe side: durations cross the wire, and the
// worker's clock never decides anything on the server.
func (w *Worker) applyBeat(ctx context.Context, held []ledger.HeldLease, reply ledger.HeartbeatReply, sent time.Time) error {
	w.mu.Lock()
	for _, fenced := range reply.Fenced {
		delete(w.deadlines, fenced.WorkOrder)
	}
	for _, renewed := range reply.Leases {
		w.deadlines[renewed.WorkOrder] = sent.Add(time.Duration(renewed.TTLMs) * time.Millisecond)
	}
	w.mu.Unlock()
	if len(reply.Fenced) == 0 {
		return nil
	}
	type lostLease struct {
		workOrder string
		fence     int64
	}
	lost := map[lostLease]bool{}
	for _, fenced := range reply.Fenced {
		lost[lostLease{fenced.WorkOrder, fenced.Fence}] = true
		w.stopFenced(ctx, fenced)
	}
	kept := make([]ledger.HeldLease, 0, len(held))
	for _, lease := range held {
		if !lost[lostLease{lease.WorkOrder, lease.Fence}] {
			kept = append(kept, lease)
		}
	}
	return w.journal.SaveLeases(ctx, kept)
}

// Wait returns when every lane this worker runs has ended.
func (w *Worker) Wait() { w.laneGroup.Wait() }

// Deadline returns when a lease may have ended, by this worker's own count.
func (w *Worker) Deadline(workOrder string) (time.Time, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	deadline, ok := w.deadlines[workOrder]
	return deadline, ok
}

// Sync reads desired state and caches it, so a restart that cannot reach the control
// plane still knows what it held.
func (w *Worker) Sync(ctx context.Context) error {
	w.mu.Lock()
	auth := ledger.WorkerAuth{WorkerID: w.identity.WorkerID, Credential: w.identity.Credential}
	since := w.desiredRev
	w.mu.Unlock()
	state, err := w.link.Desired(ctx, ledger.DesiredRequest{Auth: auth, Since: since})
	if err != nil {
		return err
	}
	if state.Unchanged {
		return nil
	}
	if err := w.journal.SaveDesired(ctx, state); err != nil {
		return err
	}
	leases := make([]ledger.HeldLease, 0, len(state.Leases))
	for _, lease := range state.Leases {
		leases = append(leases, ledger.HeldLease{WorkOrder: lease.WorkOrder, Claim: lease.ClaimID, Fence: lease.Fence})
	}
	if err := w.journal.SaveLeases(ctx, leases); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.desiredRev = state.Rev
	return nil
}

// Record journals one event under this epoch. It is durable when this returns, and
// the upload happens later, so an absent control plane costs disk and not progress.
func (w *Worker) Record(ctx context.Context, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	w.mu.Lock()
	event.Epoch = w.identity.Epoch
	w.mu.Unlock()
	event.OccurredAt = w.clock.Now().UnixMilli()
	return w.journal.Append(ctx, event)
}

// Upload sends what the ledger has not answered. A kill between the send and the
// acknowledgement repeats the same (worker, epoch, seq), and the ledger stores it
// once.
func (w *Worker) Upload(ctx context.Context) error {
	w.uploading.Lock()
	defer w.uploading.Unlock()
	w.mu.Lock()
	auth := ledger.WorkerAuth{WorkerID: w.identity.WorkerID, Credential: w.identity.Credential}
	epoch := w.identity.Epoch
	w.mu.Unlock()
	for {
		pending, err := w.journal.Pending(ctx, w.config.BatchSize)
		if err != nil || len(pending) == 0 {
			return err
		}
		crashpoint.Hit("worker.upload", "read")
		reply, err := w.link.Upload(ctx, ledger.UploadBatch{Auth: auth, Epoch: epoch, Events: pending})
		if err != nil {
			return err
		}
		crashpoint.Hit("worker.upload", "answered")
		if err := w.journal.Acknowledge(ctx, reply.HighWater, reply.Rejected); err != nil {
			return err
		}
		crashpoint.Hit("worker.upload", "acknowledged")
		if reply.HighWater < pending[len(pending)-1].Seq {
			return fmt.Errorf("the ledger stopped at %d of %d", reply.HighWater, pending[len(pending)-1].Seq)
		}
	}
}

// Run keeps the worker converged until the context ends. A failure is reported and
// retried, because a worker that lost its control plane keeps its lanes running.
func (w *Worker) Run(ctx context.Context) error {
	for {
		err := w.Start(ctx)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.config.Report(err)
		var refused *ledger.ProtocolError
		if errors.As(err, &refused) {
			return err
		}
		wait(ctx, retryEvery)
	}
	if err := w.markUnknown(ctx); err != nil {
		w.config.Report(err)
	}
	hints := make(chan int64, 1)
	go w.watch(ctx, hints)
	for {
		w.cycle(ctx)
		w.tick(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-hints:
			if err := w.Sync(ctx); err != nil && ctx.Err() == nil {
				w.config.Report(err)
			}
			w.tick(ctx)
		case <-time.After(w.pause()):
		}
	}
}

// tick is one reconcile pass that outlives its own failure.
func (w *Worker) tick(ctx context.Context) {
	if err := w.DrainSpool(ctx); err != nil && ctx.Err() == nil {
		w.config.Report(err)
	}
	if _, err := w.Reconcile(ctx); err != nil && ctx.Err() == nil {
		w.config.Report(err)
	}
}

func (w *Worker) pause() time.Duration {
	return time.Duration(w.Session().HeartbeatMs) * time.Millisecond
}

// cycle is one heartbeat and one upload. A worker another process replaced opens its
// own epoch again, and the epoch decides which of them may speak.
func (w *Worker) cycle(ctx context.Context) {
	_, err := w.Beat(ctx)
	if errors.Is(err, ledger.ErrStaleEpoch) {
		err = w.Start(ctx)
	}
	if err == nil {
		err = w.Publish(ctx)
	}
	if err == nil {
		err = w.Upload(ctx)
	}
	if err != nil && ctx.Err() == nil {
		w.config.Report(err)
		wait(ctx, min(retryEvery, w.pause()))
	}
}

// watch holds the hint stream open and reopens it after a break. A stream that
// cannot open costs latency, because the heartbeat carries the same revision.
func (w *Worker) watch(ctx context.Context, hints chan<- int64) {
	for ctx.Err() == nil {
		stream, err := w.link.Watch(ctx, ledger.WatchRequest{Auth: w.Auth(), After: w.currentRev()})
		if err != nil {
			wait(ctx, retryEvery)
			continue
		}
		for hint := range stream {
			select {
			case hints <- hint:
			default:
			}
		}
		wait(ctx, retryEvery)
	}
}

func (w *Worker) currentRev() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.desiredRev
}

func wait(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
