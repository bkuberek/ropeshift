package apitest

import (
	"ropeshift.com/ropeshift/internal/ledger"
)

// Worker registers another worker and opens its session, advertising this manifest as a worker does when it
// connects, so a read model shows what each machine reported of the agent CLIs on it.
func (w *World) Worker(id, name string, manifest ledger.Manifest) {
	w.t.Helper()
	w.join(id, name, manifest)
}

// Reporting registers another worker, opens its session, and sends one heartbeat that carries this report, as a
// worker does once it has measured its machine, so a read model shows what that machine reported.
func (w *World) Reporting(id, name string, report ledger.ResourceReport) {
	w.t.Helper()
	auth, epoch := w.join(id, name, ledger.Manifest{})
	beat := ledger.Heartbeat{Auth: auth, Epoch: epoch, Protocol: ledger.ProtocolVersion, Nonce: "report", Report: &report}
	if _, err := w.plane.Heartbeat(w.ctx, beat); err != nil {
		w.t.Fatal(err)
	}
}

// join registers a worker and opens its session, and returns what the worker speaks as.
func (w *World) join(id, name string, manifest ledger.Manifest) (ledger.WorkerAuth, int64) {
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
	session, err := w.plane.OpenSession(w.ctx, ledger.SessionRequest{Auth: auth, Protocol: ledger.ProtocolVersion, Manifest: manifest})
	if err != nil {
		w.t.Fatal(err)
	}
	return auth, session.Epoch
}
