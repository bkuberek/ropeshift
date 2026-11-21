package server_test

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// connectSoloWorker registers a worker with a token the daemon's own worker would make, and opens its session on the socket.
func connectSoloWorker(t *testing.T, d *apitest.Daemon, id string) {
	t.Helper()
	solo := ledger.Actor{Kind: ledger.ActorSystem, ID: "solo"}
	token, err := control.NewWorkers(d.Store, apitest.Scope, control.WorkerOptions{}).CreateJoinToken(t.Context(), solo)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := d.Client.Register(t.Context(), ledger.Registration{JoinToken: token, WorkerID: id, Name: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workerOf(t, d, credential.Credential).OpenSession(t.Context(), id, ledger.SessionRequest{Protocol: ledger.ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
}

// orchestratorOf reads a worker's orchestrator from the installation's records, which a daemon with no member has no
// session to read it through.
func orchestratorOf(t *testing.T, d *apitest.Daemon, worker string) (ledger.Orchestrator, error) {
	t.Helper()
	var row ledger.Orchestrator
	err := d.Store.Installation(apitest.Scope).Read(t.Context(), func(tx ports.SharedReadTx) error {
		var err error
		row, err = tx.Workers().Orchestrator(worker)
		return err
	})
	return row, err
}

func wantRunning(t *testing.T, d *apitest.Daemon, worker string) {
	t.Helper()
	row, err := orchestratorOf(t, d, worker)
	if err != nil || row.Desired != ledger.OrchestratorRunning || row.Run != 1 {
		t.Fatalf("worker %s's orchestrator is %+v, %v, want running on run 1", worker, row, err)
	}
}

func wantNoOrchestrator(t *testing.T, d *apitest.Daemon, worker string) {
	t.Helper()
	if row, err := orchestratorOf(t, d, worker); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("worker %s has an orchestrator nobody asked for: %+v, %v", worker, row, err)
	}
}

// TestADaemonAsksForNoOrchestratorUntilThePassphraseIsSet holds the production wiring of a worker's orchestrator: the
// worker the daemon runs connects before the first passphrase exists, as it does on a fresh installation, and gets no
// orchestrator until the passphrase set on the socket, which needs no session, makes a member. A worker that connects
// after it gets one with its session (FR-ORCH-1).
func TestADaemonAsksForNoOrchestratorUntilThePassphraseIsSet(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{NoMember: true, StartsOrchestrator: true, LocalPassphrase: true})
	connectSoloWorker(t, d, "worker-first")
	wantNoOrchestrator(t, d, "worker-first")
	if _, err := d.Client.SetPassphrase(t.Context(), api.SetPassphrase{Passphrase: apitest.Passphrase}); err != nil {
		t.Fatal(err)
	}
	wantRunning(t, d, "worker-first")
	connectSoloWorker(t, d, "worker-second")
	wantRunning(t, d, "worker-second")
}

// TestADaemonThatHoldsTheOrchestratorAsksForNoneWithThePassphrase holds that the one setting a harness turns on to
// start an orchestrator only when a test asks also keeps the first passphrase from starting one.
func TestADaemonThatHoldsTheOrchestratorAsksForNoneWithThePassphrase(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{NoMember: true, LocalPassphrase: true})
	connectSoloWorker(t, d, "worker-held")
	if _, err := d.Client.SetPassphrase(t.Context(), api.SetPassphrase{Passphrase: apitest.Passphrase}); err != nil {
		t.Fatal(err)
	}
	connectSoloWorker(t, d, "worker-later")
	wantNoOrchestrator(t, d, "worker-held")
	wantNoOrchestrator(t, d, "worker-later")
}
