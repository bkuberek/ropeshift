package contract

import (
	"errors"

	"ropeshift.com/ropeshift/internal/adapters/localauth"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// OrchestratorEnrollChecks hold that no orchestrator is asked for until the first passphrase is set. That passphrase
// needs no session, so it is safe only while no agent runs before it: a worker's session asks for its orchestrator once
// a member exists, and the first passphrase asks for the orchestrator of each worker that already connected, in the
// transaction that stores it (FR-ORCH-1).
var OrchestratorEnrollChecks = []Check{
	{"OE1", "a worker's session opened before the first passphrase asks for no orchestrator", checkNoOrchestratorBeforeTheFirstPassphrase},
	{"OE2", "the first passphrase asks for the orchestrator of each registered worker that has none, and leaves one a member stopped stopped", checkTheFirstPassphraseStartsOrchestrators},
	{"OE3", "a worker's session opened after the first passphrase asks for its orchestrator", checkASessionAfterTheFirstPassphraseStartsOne},
	{"OE4", "a first passphrase whose orchestrator start fails is not stored, and the start stands with it when it is set again", checkTheFirstPassphraseAndItsStartStandTogether},
}

// enrollHash keeps the checks fast. A stored hash carries its own costs, so nothing else changes with them.
var enrollHash = localauth.Params{Time: 1, MemoryKiB: 8, Threads: 1}

// errStartFailure is what a check's failing transaction refuses an orchestrator's request with.
var errStartFailure = errors.New("the orchestrator's request could not be recorded")

// enrollRig is a plane that asks for an orchestrator with each worker's session, and the sessions that set the first
// passphrase on the same installation records.
type enrollRig struct {
	r        *Rig
	store    *Interposed
	plane    *control.Workers
	sessions *control.Sessions
}

func newEnrollRig(r *Rig) *enrollRig {
	store := &Interposed{Store: r.Store()}
	return &enrollRig{
		r: r, store: store,
		plane: control.NewWorkers(store, r.Scope, control.WorkerOptions{
			HeartbeatMs: statusHeartbeatMs, LeaseTTLMs: TTLMs, StartsOrchestrator: true,
		}),
		sessions: control.NewSessions(store, localauth.New(store, enrollHash)).StartingOrchestrators(true),
	}
}

// register adds a worker and returns what it proves itself with.
func (e *enrollRig) register(name string) (ledger.WorkerAuth, error) {
	token, err := e.plane.CreateJoinToken(e.r.Ctx, theMember)
	if err != nil {
		return ledger.WorkerAuth{}, err
	}
	worker := e.r.ID(name)
	credential, err := e.plane.Register(e.r.Ctx, ledger.Registration{JoinToken: token, WorkerID: worker, Name: name})
	return ledger.WorkerAuth{WorkerID: worker, Credential: credential.Credential}, err
}

// connect registers a worker and opens its session.
func (e *enrollRig) connect(name string) (string, error) {
	auth, err := e.register(name)
	if err != nil {
		return "", err
	}
	_, err = e.plane.OpenSession(e.r.Ctx, ledger.SessionRequest{Auth: auth, Protocol: ledger.ProtocolVersion, Manifest: UsableManifest})
	return auth.WorkerID, err
}

func (e *enrollRig) enroll() error {
	_, err := e.sessions.Enroll(e.r.Ctx, e.r.Scope, ports.Proof{Passphrase: goodPassphrase}, "")
	return err
}

// orchestrator reads a worker's orchestrator, or ports.ErrNotFound.
func (e *enrollRig) orchestrator(worker string) (ledger.Orchestrator, error) {
	var row ledger.Orchestrator
	err := e.store.Installation(e.r.Scope).Read(e.r.Ctx, func(tx ports.SharedReadTx) error {
		var err error
		row, err = tx.Workers().Orchestrator(worker)
		return err
	})
	return row, err
}

// requests lists who asked for each orchestrator, by the worker whose orchestrator it was.
func (e *enrollRig) requests() (map[string][]ledger.Actor, error) {
	events, err := sharedEvents(e.r, e.store)
	if err != nil {
		return nil, err
	}
	asked := map[string][]ledger.Actor{}
	for _, event := range events {
		if event.Type == ledger.EventOrchestratorDesired {
			asked[event.Subject.ID] = append(asked[event.Subject.ID], event.Actor)
		}
	}
	return asked, nil
}

// wantRunning holds that a worker's orchestrator is asked to run on its first run.
func (e *enrollRig) wantRunning(worker string) error {
	row, err := e.orchestrator(worker)
	if err != nil {
		return Violatedf("worker %s has no orchestrator: %v", worker, err)
	}
	if row.Desired != ledger.OrchestratorRunning || row.Run != 1 {
		return Violatedf("worker %s's orchestrator is %+v, want running on run 1", worker, row)
	}
	return nil
}

// wantNone holds that nothing records an orchestrator for the worker.
func (e *enrollRig) wantNone(worker string) error {
	if row, err := e.orchestrator(worker); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("worker %s has an orchestrator that nobody asked for: %+v, %v", worker, row, err)
	}
	asked, err := e.requests()
	if err != nil {
		return err
	}
	if len(asked[ledger.OrchestratorOf(worker)]) != 0 {
		return Violatedf("an orchestrator was asked for worker %s: %v", worker, asked[ledger.OrchestratorOf(worker)])
	}
	return nil
}

func checkNoOrchestratorBeforeTheFirstPassphrase(r *Rig) error {
	e := newEnrollRig(r)
	worker, err := e.connect("worker")
	if err != nil {
		return err
	}
	return e.wantNone(worker)
}

func checkTheFirstPassphraseStartsOrchestrators(r *Rig) error {
	e := newEnrollRig(r)
	connected, err := e.connect("connected")
	if err != nil {
		return err
	}
	stopped, err := e.connect("stopped")
	if err != nil {
		return err
	}
	registered, err := e.register("registered")
	if err != nil {
		return err
	}
	for _, state := range []string{ledger.OrchestratorRunning, ledger.OrchestratorStopped} {
		if _, err := e.plane.SetOrchestrator(r.Ctx, stopped, state, theMember); err != nil {
			return err
		}
	}
	if err := e.wantNone(connected); err != nil {
		return err
	}
	if err := e.enroll(); err != nil {
		return err
	}
	for _, worker := range []string{connected, registered.WorkerID} {
		if err := e.wantRunning(worker); err != nil {
			return err
		}
	}
	row, err := e.orchestrator(stopped)
	if err != nil || row.Desired != ledger.OrchestratorStopped || row.Run != 1 {
		return Violatedf("worker %s's orchestrator, which a member stopped, is %+v, %v, want it stopped on run 1", stopped, row, err)
	}
	asked, err := e.requests()
	if err != nil {
		return err
	}
	if got := asked[ledger.OrchestratorOf(connected)]; len(got) != 1 || got[0].Kind != ledger.ActorSystem {
		return Violatedf("the first passphrase's request for worker %s's orchestrator is %v, want one, from the system", connected, got)
	}
	if got := asked[ledger.OrchestratorOf(stopped)]; len(got) != 2 || got[0] != theMember || got[1] != theMember {
		return Violatedf("a member's start and stop of worker %s's orchestrator are %v, and the first passphrase added to them", stopped, got)
	}
	return nil
}

func checkASessionAfterTheFirstPassphraseStartsOne(r *Rig) error {
	e := newEnrollRig(r)
	if err := e.enroll(); err != nil {
		return err
	}
	worker, err := e.connect("worker")
	if err != nil {
		return err
	}
	return e.wantRunning(worker)
}

// failingRequest is a transaction that cannot record an orchestrator's request.
type failingRequest struct{ ports.SharedTx }

func (f failingRequest) Append(draft ledger.EventDraft) (ledger.Event, error) {
	if draft.Type == ledger.EventOrchestratorDesired {
		return ledger.Event{}, errStartFailure
	}
	return f.SharedTx.Append(draft)
}

func checkTheFirstPassphraseAndItsStartStandTogether(r *Rig) error {
	e := newEnrollRig(r)
	worker, err := e.connect("worker")
	if err != nil {
		return err
	}
	e.store.Within = func(tx ports.SharedTx) ports.SharedTx { return failingRequest{tx} }
	if err := e.enroll(); !errors.Is(err, errStartFailure) {
		return Violatedf("a first passphrase whose orchestrator could not be asked for was set: %v", err)
	}
	e.store.Within = nil
	if set, err := e.sessions.Enrolled(r.Ctx, r.Scope); err != nil || set {
		return Violatedf("a passphrase stands after its set failed: %v, %v", set, err)
	}
	if err := e.wantNone(worker); err != nil {
		return err
	}
	if err := e.enroll(); err != nil {
		return err
	}
	return e.wantRunning(worker)
}
