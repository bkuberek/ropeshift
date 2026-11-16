package apitest

import (
	"ropeshift.com/ropeshift/internal/ledger"
)

// Worker registers another worker and opens its session, advertising this manifest as a worker does when it
// connects, so a read model shows what each machine reported of the agent CLIs on it.
func (w *World) Worker(id, name string, manifest ledger.Manifest) {
	w.t.Helper()
	token, err := w.plane.CreateJoinToken(w.ctx, worldMember)
	if err != nil {
		w.t.Fatal(err)
	}
	credential, err := w.plane.Register(w.ctx, ledger.Registration{JoinToken: token, WorkerID: id, Name: name})
	if err != nil {
		w.t.Fatal(err)
	}
	auth := ledger.WorkerAuth{WorkerID: id, Credential: credential.Credential}
	if _, err := w.plane.OpenSession(w.ctx, ledger.SessionRequest{Auth: auth, Protocol: ledger.ProtocolVersion, Manifest: manifest}); err != nil {
		w.t.Fatal(err)
	}
}
