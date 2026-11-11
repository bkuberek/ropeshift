package contract

import (
	"encoding/json"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/localauth"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// TestSharedRecordEventsLandInTheInstallationLog makes every change to a record every project shares, in
// a project of an installation, and reads where each event went: the installation's log holds each one,
// and the project's log holds none (ADR-0021).
func TestSharedRecordEventsLandInTheInstallationLog(t *testing.T) {
	t.Parallel()
	r := NewRig(t, SQLiteProject)
	store := r.Store()
	ctx := r.Ctx
	identity := localauth.New(store, localauth.Params{Time: 1, MemoryKiB: 64, Threads: 1})
	sessions := control.NewSessions(store, identity)
	if err := sessions.Enroll(ctx, r.Scope, ports.Proof{Passphrase: "a passphrase only a person knows"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.SignIn(ctx, r.Scope, ports.Proof{Passphrase: "a passphrase only a person knows"}, 0); err != nil {
		t.Fatal(err)
	}
	plane := control.NewWorkers(store, r.Scope, control.WorkerOptions{})
	token, err := plane.CreateJoinToken(ctx, theMember)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := plane.Register(ctx, ledger.Registration{JoinToken: token, WorkerID: "worker-1", Name: "w"})
	if err != nil {
		t.Fatal(err)
	}
	auth := ledger.WorkerAuth{WorkerID: "worker-1", Credential: credential.Credential}
	session, err := plane.OpenSession(ctx, ledger.SessionRequest{Auth: auth, Protocol: ledger.ProtocolVersion, Manifest: subscriptionManifest})
	if err != nil {
		t.Fatal(err)
	}
	autonomy := control.NewAutonomy(store)
	if err := autonomy.SetLevel(ctx, r.Scope, "worker-1", authority.LevelManual, theMember); err != nil {
		t.Fatal(err)
	}
	if _, err := autonomy.OpenWindow(ctx, r.Scope, "worker-1", "window-1", 60_000, theMember); err != nil {
		t.Fatal(err)
	}
	row, err := plane.SetOrchestrator(ctx, "worker-1", ledger.OrchestratorRunning, theMember)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := json.Marshal(workorders.GenerationBinding{SessionID: "s-1", ModelResolved: "model-o"})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := plane.Upload(ctx, ledger.UploadBatch{Auth: auth, Epoch: session.Epoch, Events: []ledger.UploadEvent{
		{Epoch: session.Epoch, Seq: 1, Type: ledger.UploadGenerationBound, Lane: row.Lane(), Generation: 1, Payload: bound},
	}})
	if err != nil || len(reply.Rejected) != 0 {
		t.Fatalf("the orchestrator's binding: %+v, %v", reply, err)
	}

	shared, err := r.SharedEvents(store)
	if err != nil {
		t.Fatal(err)
	}
	project, err := r.Events(store, r.Scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{
		control.EventSignedIn, "member.proof_set", "worker.join_token_created", "worker.registered", "worker.session_opened",
		authority.EventAutonomySet, authority.EventWindowOpened, ledger.EventOrchestratorDesired, ledger.EventOrchestratorReported,
	} {
		has := func(e ledger.Event) bool { return e.Type == kind }
		if !slices.ContainsFunc(shared, has) {
			t.Errorf("the installation's log has no %s", kind)
		}
		if slices.ContainsFunc(project, has) {
			t.Errorf("the project's log holds %s, an event of a record every project shares", kind)
		}
	}
	got, err := plane.Orchestrator(ctx, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Settings) == 0 {
		t.Errorf("the orchestrator's record keeps no settings from its binding: %+v", got)
	}
}
